// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// outFrozenExec keeps out_time on one position for every report (a subtitle gap: out_time follows the
// most-behind stream and freezes while the encode goes on). frames says whether frame= grows with each report;
// sizeStep is how far total_size= grows per report (0: no total_size=). Each report takes a little real time (for
// the stall stop) and advances the fake clock by tick (for the 30 s warning). With ticks 0 it reports until the
// stall stop cancels it.
type outFrozenExec struct {
	*media.FakeExec
	clock    *fakeClockAt
	tick     time.Duration
	ticks    int
	frames   bool
	sizeStep int64
	gap      time.Duration // real time per report (0: 50 ms)
}

func (x *outFrozenExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	gap := x.gap
	if gap == 0 {
		gap = 50 * time.Millisecond
	}
	for i := 0; x.ticks == 0 || i < x.ticks; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(gap):
		}
		if x.frames {
			onLine(fmt.Sprintf("frame=%d", i))
		}
		if x.sizeStep > 0 {
			onLine(fmt.Sprintf("total_size=%d", x.sizeStep*int64(i+1)))
		}
		onLine("out_time_us=5000000")
		onLine("progress=continue")
		x.clock.add(x.tick)
	}
	return x.FakeExec.Stream(ctx, onLine, name, args...) // then the new file, as the fake makes it
}

// encodeLines returns the job's encode-section log lines, the warnings and the stall stops among them.
func encodeLines(t *testing.T, s *scenario) (warn int, stopped bool) {
	t.Helper()
	lines, _ := s.store.LogLines(s.id)
	for _, l := range lines {
		if l.Section != "encode" {
			continue
		}
		if l.Level == jobs.LevelWarning && strings.Contains(l.Text, "has not moved for 30s") {
			warn++
		}
		if strings.Contains(l.Text, "no progress for") {
			stopped = true
		}
	}
	return warn, stopped
}

// Bug 2: out_time frozen for longer than StallAfter, with frame= (or total_size=) still growing within twice the
// source's size, is an encode that is alive: not stopped, and no "has not moved" warning, though the fake clock runs
// past 30 s without out_time moving. The scenario's source is 14 bytes, so the bytes stay within the bound for 28
// reports; 24 reports 100 ms apart (about 2.4 s real, past the 2 s StallAfter) keep them there. The ceiling is out
// of the way (its own test covers it), and a 100 ms report gap sits 20 times inside the stall allowance.
func TestFrozenOutTimeIsAliveWhileFramesOrBytesGrow(t *testing.T) {
	for _, c := range []struct {
		name     string
		frames   bool
		sizeStep int64
	}{{"frames grow", true, 0}, {"bytes grow", false, 1}, {"both grow", true, 1}} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
			w := s.worker(Replace)
			w.Now = clock.now
			w.StallAfter = 2 * time.Second // real time: a report can come late under load
			w.FrozenPositionFactor = 1000  // the ceiling has its own test; out of the way here
			w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second, ticks: 24, frames: c.frames, sizeStep: c.sizeStep, gap: 100 * time.Millisecond}
			if _, err := w.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if j := s.job(); j.Status != jobs.Done {
				t.Fatalf("status %s %q, want done: the encode was alive", j.Status, j.Error)
			}
			if warn, stopped := encodeLines(t, s); warn != 0 || stopped {
				t.Fatalf("%d 'has not moved' warnings, stopped %v; want none", warn, stopped)
			}
		})
	}
}

// Bug 2, the control: out_time, frame= and total_size all frozen is still stopped at StallAfter, and the warning
// is written (the fake clock runs past 30 s).
func TestFullyFrozenEncodeIsStillStopped(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") {
		t.Fatalf("status %s %q, want failed with no progress for 300ms", j.Status, j.Error)
	}
	if warn, _ := encodeLines(t, s); warn != 1 {
		t.Fatalf("%d 'has not moved' warnings, want 1", warn)
	}
	s.assertOriginalSafe()
}

// Bug 2, the bound: frames still growing with out_time frozen is stopped once total_size passes twice the source's
// size (a runaway output is not progress); the stall stop applies, and the warning is written.
func TestFrozenOutTimeStopsPastTheOutputBound(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second, frames: true, sizeStep: 1000}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	j := s.job()
	// Past the bound the stop says why: the new file is over twice the original, so the words are not "no progress".
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "more than twice the original's size") || strings.Contains(j.Error, "no progress") {
		t.Fatalf("status %s %q, want failed: the new file is more than twice the original's size", j.Status, j.Error)
	}
	if warn, _ := encodeLines(t, s); warn != 1 {
		t.Fatalf("%d 'has not moved' warnings, want 1", warn)
	}
	lines, _ := s.store.LogLines(s.id)
	said := false
	for _, l := range lines {
		if l.Section == "encode" && strings.Contains(l.Text, "more than twice the original's size") && strings.Contains(l.Text, "stopping it") {
			said = true
		}
		if l.Section == "encode" && strings.Contains(l.Text, "no progress for") {
			t.Errorf("past the bound the log says %q; it should say the output is over twice the original", l.Text)
		}
	}
	if !said {
		t.Error("no log line says the stop was because the output passed twice the original's size")
	}
	s.assertOriginalSafe()
}

// The time ceiling: frames keep growing within the size bound, so the encode is alive at every report, but out_time
// has not moved for longer than FrozenPositionFactor x StallAfter (2 x 1 s here). It is stopped at the ceiling. The
// encode runs 5 s, so without the ceiling it would finish: the test fails if the ceiling is removed. Reports are 50 ms
// apart, far inside the 1 s StallAfter, so the plain stall stop cannot be what ends it.
func TestFrozenPositionCeilingStopsAliveEncode(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = time.Second
	w.FrozenPositionFactor = 2
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: time.Second, ticks: 100, frames: true}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 2s") {
		t.Fatalf("status %s %q, want failed with no progress for 2s (the frozen-position ceiling)", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// The ceiling's default must sit well above the real subtitle gap (195 s seen).
func TestDefaultFrozenPositionCeilingClearsASubtitleGap(t *testing.T) {
	if got := frozenPositionFactor * DefaultStallAfter; got < 3*195*time.Second {
		t.Fatalf("default frozen-position ceiling %s, want well above the 195 s subtitle gap", got)
	}
}

// The size bound is twice the source, and a source of unknown size has none: pins the factor and SizeBefore == 0.
func TestOutputWithinTwiceTheSource(t *testing.T) {
	for _, c := range []struct {
		name          string
		before, total int64
		want          bool
	}{
		{"unknown source size", 0, 0, false},
		{"unknown source size, bytes written", 0, 10, false},
		{"empty output", 14, 0, true},
		{"at twice the source", 14, 28, true},
		{"one byte past twice", 14, 29, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := outputWithin(c.before, c.total); got != c.want {
				t.Fatalf("outputWithin(%d, %d) = %v, want %v", c.before, c.total, got, c.want)
			}
		})
	}
}
