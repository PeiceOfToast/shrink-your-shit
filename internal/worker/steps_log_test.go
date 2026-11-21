// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// L2: each step the workflow ran has its own section, in run order, before the encode, with one decision line.
func TestStepSectionsFromTheWorkflow(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"step:1:Convert video", "step:2:Audio", "step:3:Subtitles", "step:4:File type"}
	seen := map[string]bool{}
	var order []string
	for _, l := range lines {
		key := l.Section
		if !strings.HasPrefix(key, "step:") && key != "input" && key != "encode" {
			continue
		}
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}
	steps := make([]string, 0, len(want))
	for _, o := range order {
		if strings.HasPrefix(o, "step:") {
			steps = append(steps, o)
		}
	}
	if strings.Join(steps, "|") != strings.Join(want, "|") {
		t.Fatalf("step sections %v, want %v", steps, want)
	}
	if !slices.Contains(order, "input") || slices.Index(order, "input") > slices.Index(order, "step:1:Convert video") ||
		slices.Index(order, "step:4:File type") > slices.Index(order, "encode") {
		t.Fatalf("order %v: the steps go between the input and the encode", order)
	}
	for _, sec := range want {
		var got []jobs.LogLine
		for _, l := range lines {
			if l.Section == sec {
				got = append(got, l)
			}
		}
		if len(got) != 1 || got[0].Stream != "app" || got[0].Level != jobs.LevelInfo || !strings.HasSuffix(got[0].Text, ".") {
			t.Fatalf("%s: %+v", sec, got)
		}
	}
	v := ""
	for _, l := range lines {
		if l.Section == "step:1:Convert video" {
			v = l.Text
		}
	}
	if !strings.Contains(v, "HEVC") {
		t.Fatalf("the video decision does not say what it converts to: %q", v)
	}
}

// R4: a step that did nothing still has its section, and says why.
func TestStepThatDidNothingStillHasItsSection(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams = s.srcSpec.streams[:1] // the video only
	s.outSpec.streams = s.outSpec.streams[:1]
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	w.RunOne(context.Background())
	lines, _ := s.store.LogLines(s.id)
	found := ""
	for _, l := range lines {
		if l.Section == "step:2:Audio" {
			found = l.Text
		}
	}
	if found != "There is no audio." {
		t.Fatalf("the audio step's line %q, lines %+v", found, lines)
	}
}

// R12: two steps with one name are numbered, the second "Name (2)".
func TestDuplicateStepNamesAreNumbered(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.writeStepSections(s.id, []rules.StepDecision{
		{Label: rules.StepCheck, Outcome: "ran", Decision: "Is it 4K? → Yes."},
		{Label: rules.StepCheck, Outcome: "ran", Decision: "Is it HDR? → No."},
		{Label: rules.StepAudio, Outcome: "ran", Decision: "Keep every track as it is."},
	})
	lines, _ := s.store.LogLines(s.id)
	var names []string
	for _, l := range lines {
		names = append(names, l.Section)
	}
	want := []string{"step:1:Check the file", "step:2:Check the file (2)", "step:3:Audio"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("sections %v, want %v", names, want)
	}
}

// R9: after 30 s of frozen media time the encode section says so once (warning, app), and one line says when it
// moves again. The clock is the worker's own, so no time really passes.
func TestStallWarningAfterFrozenMediaTime(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.stallClock = clock.now
	w.Exec = &frozenThenMovingExec{FakeExec: s.exec, clock: clock}
	w.RunOne(context.Background())
	lines, _ := s.store.LogLines(s.id)
	var warn, again int
	var texts []string
	for _, l := range lines {
		if l.Section != "encode" {
			continue
		}
		if l.Level == jobs.LevelWarning && l.Stream == "app" && strings.Contains(l.Text, "has not moved for 30s") {
			warn++
			texts = append(texts, l.Text)
		}
		if l.Level == jobs.LevelInfo && l.Stream == "app" && strings.Contains(l.Text, "moving again") {
			again++
			texts = append(texts, l.Text)
		}
	}
	if warn != 1 || again != 1 {
		t.Fatalf("%d warnings, %d resumed lines, want 1 and 1: %v", warn, again, texts)
	}
}

// L2 summary counters: the encode, the playback test and the picture comparison are the job's ffmpeg runs, and
// what ffmpeg reported writing is the bytes it wrote.
func TestRunCountersAreRecorded(t *testing.T) {
	s := newScenario(t)
	s.progressOut = "frame=10\ntotal_size=123456\nout_time_us=50000000\nprogress=continue\n"
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	passes, written, ok, err := s.store.JobStats(s.id)
	if err != nil || !ok {
		t.Fatalf("stats ok=%v err=%v", ok, err)
	}
	// every ffmpeg the job ran, but not the once-per-worker "has it the ssim filter" question
	if want := s.count("ffmpeg ") - s.count("-filters"); passes != want {
		t.Errorf("passes %d, want %d (the ffmpeg calls the fake saw)", passes, want)
	}
	if passes < 3 { // at least the encode, the playback test and the picture comparison
		t.Errorf("passes %d: the checks are not counted", passes)
	}
	if written != 123456 {
		t.Errorf("bytesWritten %d, want 123456", written)
	}
}

// fakeClockAt is a clock a test moves by hand.
type fakeClockAt struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClockAt) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClockAt) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// frozenThenMovingExec reports one media position, keeps reporting it while the fake clock runs past the
// warning threshold, then moves on: the whole of R9's story in one encode, with no real waiting.
type frozenThenMovingExec struct {
	*media.FakeExec
	clock *fakeClockAt
}

func (x *frozenThenMovingExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	onLine("out_time_us=5000000")
	onLine("progress=continue")
	x.clock.add(20 * time.Second) // frozen, but not yet 30 s
	onLine("out_time_us=5000000")
	onLine("progress=continue")
	x.clock.add(15 * time.Second) // 35 s: the warning
	onLine("out_time_us=5000000")
	onLine("progress=continue")
	x.clock.add(10 * time.Second) // still frozen: no second warning
	onLine("out_time_us=5000000")
	onLine("progress=continue")
	x.clock.add(1 * time.Second)
	onLine("out_time_us=6000000") // moving again: one line says so
	onLine("progress=continue")
	return x.FakeExec.Stream(ctx, onLine, name, args...) // then the new file, as the fake makes it
}
