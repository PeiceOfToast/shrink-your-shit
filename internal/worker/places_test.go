// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// mixedWorker is a scenario worker whose 8-bit HEVC plans encode on the card (hevc_nvenc) and whose 10-bit
// ones fall to the processor (the 10-bit test has not passed here): the queue's own files decide which.
// places: the Files at once places it takes (0: the setting is off, as before).
func (s *scenario) mixedWorker(id string, places int) *Worker {
	w := s.worker(Replace)
	w.ID = id
	w.Poll = 10 * time.Millisecond
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	w.Encoder = func(codec string, need10 bool, _ *rules.HDR) (string, string) {
		if codec == "libx265" && !need10 {
			return "hevc_nvenc", ""
		}
		return "", ""
	}
	w.GPUSlots = func() int { return 1 }
	if places > 0 {
		w.FilePlaces = func() int { return places }
	}
	return w
}

// mixedQueue enqueues names after the scenario's own job and returns every id in queue order. Every file but
// the graphics one (8-bit HEVC on the card) is a 10-bit source: those convert on the processor.
func (s *scenario) mixedQueue(names ...string) []int64 {
	s.moreSrcs = map[string]bool{}
	s.srcSpecs = map[string]spec{s.src: tenBitSourceSpec()}
	ids := []int64{s.id}
	for _, name := range names {
		p := filepath.Join(s.dir, name)
		if err := os.WriteFile(p, []byte("original-bytes"), 0o644); err != nil {
			s.t.Fatal(err)
		}
		s.moreSrcs[p] = true
		if name != "graphics.mkv" {
			s.srcSpecs[p] = tenBitSourceSpec()
		}
		id, err := s.store.Enqueue(jobs.Job{Library: s.dir, Path: p, DurationSec: 100, SizeBefore: 14})
		if err != nil {
			s.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// tenBitSourceSpec is sourceSpec at 10 bits: its plan needs the card's 10-bit test, which has not passed.
func tenBitSourceSpec() spec {
	return spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("h264", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
}

// startPool runs a pool whose size the test changes, and returns it with a stop function.
func startPool(t *testing.T, s *scenario, size *atomic.Int64, places int) (*Pool, func()) {
	t.Helper()
	p := &Pool{
		New:   func(n int) *Worker { return s.mixedWorker(fmt.Sprintf("w%d", n), places) },
		Size:  func() int { return int(size.Load()) },
		Every: 20 * time.Millisecond,
		Cap:   16,
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { p.Run(ctx); close(stopped) }()
	return p, func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(60 * time.Second):
			t.Fatalf("the pool did not stop (%d still converting)", p.Busy())
		}
	}
}

// Slice 18, the setting off: a graphics job whose card encode fails converts on the processor at once, as
// today. There are no places to wait for, so it must not wait at all (an earlier version waited on none and
// hung every fall-back on an install with the setting off).
func TestGraphicsFallbackDoesNotWaitWhenTheSettingIsOff(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_nvenc" // its graphics encode fails: the processor takes over
	w := s.mixedWorker("w1", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan struct{})
	var ran bool
	var err error
	go func() { ran, err = w.RunOne(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the fall-back waited for a Files at once place although the setting is off")
	}
	if err != nil || !ran {
		t.Fatalf("ran %v, err %v: the job converts on the processor", ran, err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("status %s %q, want done", j.Status, j.Error)
	}
	if n := FilePlacesInUse(); n != 0 {
		t.Errorf("Files at once places in use with the setting off: %d, want 0", n)
	}
}

// waitFor waits up to a minute for ok. These tests plan real (fake-ffmpeg) jobs on a loaded box, where the
// first encode can take several seconds to start, well past the short eventually budget.
func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for i := 0; i < 6000; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// The pool's cap leaves room for the workers of the card's slots beside Files at once: a graphics job runs on
// its own worker while every Files at once place is taken, so the pool needs workers past MaxParallel.
func TestPoolRunsPastMaxParallelUpToItsCap(t *testing.T) {
	s := newScenario(t)
	var size atomic.Int64
	size.Store(99)
	p := &Pool{
		New: func(n int) *Worker {
			w := s.worker(Replace)
			w.ID = fmt.Sprintf("w%d", n)
			w.Poll = 10 * time.Millisecond
			w.Paused = func() bool { return true } // polls without taking work
			return w
		},
		Size:  func() int { return int(size.Load()) },
		Every: 20 * time.Millisecond,
		Cap:   MaxParallel + 8,
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { p.Run(ctx); close(stopped) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(60 * time.Second):
			t.Fatalf("the pool did not stop (%d still working)", p.Running())
		}
	}()
	waitFor(t, func() bool { return p.Running() == MaxParallel+8 }, "MaxParallel+8 workers")
}

// counter counts the encodes that started, by kind (they hang until the pool stops).
type counter struct {
	mu                sync.Mutex
	processor, onCard int
}

func (c *counter) hang(s *scenario) func(name string, args []string) bool {
	return func(name string, args []string) bool {
		if name != "ffmpeg" {
			return false
		}
		switch {
		case s.has(args, "libx265"):
			c.mu.Lock()
			c.processor++
			c.mu.Unlock()
			return true
		case s.has(args, "hevc_nvenc"):
			c.mu.Lock()
			c.onCard++
			c.mu.Unlock()
			return true
		}
		return false
	}
}

func (c *counter) counts() (processor, onCard int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processor, c.onCard
}

// Slice 18, the setting on: two processor jobs fill Files at once, a third processor job waits in the queue,
// and the graphics job behind it starts on the card. The worker added for the card hands the third job back
// (it would convert on the processor, and every place is in use) and takes the graphics job past it; the two
// processor jobs keep converting, so no third processor encode runs. A handed-back job that was waiting to be
// tried again keeps its earlier failure's summary, words and try count.
func TestGraphicsJobStartsBesideBusyFilesAtOnce(t *testing.T) {
	s := newScenario(t)
	ids := s.mixedQueue("p2.mkv", "p3.mkv", "graphics.mkv")
	// The job this test hands back has failed once already and is waiting to be tried again: the claim keeps
	// its words on the row for the hand-back to put back.
	prev := jobs.FailureSummary{Step: 0, StepCount: 1, ExitCode: 1, FfmpegRan: true, KeyLines: []string{"Invalid data found when processing input"}}
	if err := s.store.SetFailureSummary(ids[2], prev); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetError(ids[2], "The disk was full when it last ran.", "disk_full"); err != nil {
		t.Fatal(err)
	}
	c := &counter{}
	s.hang = c.hang(s)
	var size atomic.Int64
	size.Store(2)
	_, stop := startPool(t, s, &size, 2)
	stopped := false
	defer func() {
		if !stopped { // a fatal above still ends the pool, and the workers with it
			stop()
		}
	}()
	waitFor(t, func() bool { p, _ := c.counts(); return p == 2 }, "the two processor jobs converting")
	size.Store(3) // the worker added for the card's slot
	waitFor(t, func() bool { _, g := c.counts(); return g == 1 }, "the graphics job converting")
	if p, _ := c.counts(); p != 2 {
		t.Fatalf("processor encodes %d, want 2: the graphics job runs beside them, not a third processor job", p)
	}
	if j, _ := s.store.Get(ids[3]); j.Status != jobs.Running {
		t.Errorf("the graphics job is %s, want running", j.Status)
	}
	// The hand-back restores the words before it writes its note, so the note proves the assertions below are
	// about what the hand-back kept, not about the seeded row still waiting in the queue.
	waitForLog(t, s, ids[2], "not started: it would convert on the processor")
	waitFor(t, func() bool {
		j, _ := s.store.Get(ids[2])
		return j.Status == jobs.Queued && j.Error == "The disk was full when it last ran." && j.ErrorCode == "disk_full"
	}, "the third processor job handed back with the words it was waiting with")
	j, _ := s.store.Get(ids[2])
	if j.Attempts != 0 {
		t.Errorf("the handed-back job counts %d tries, want 0: a hand-back is not a try", j.Attempts)
	}
	if got, err := s.store.FailureOf(ids[2]); err != nil || got == nil ||
		got.ExitCode != prev.ExitCode || len(got.KeyLines) != 1 || got.KeyLines[0] != prev.KeyLines[0] {
		t.Errorf("the handed-back job's failure summary: %+v (err %v), want %+v", got, err, prev)
	}
	if n := FilePlacesInUse(); n != 2 {
		t.Errorf("Files at once places in use: %d, want the two processor jobs", n)
	}
	// Every encode is cancelled by the stop, so every place must be back: a job's place is released on every
	// way out of process, and one missed Release would show here (or, before, only in a later test).
	stop()
	stopped = true
	if n := FilePlacesInUse(); n != 0 {
		t.Errorf("Files at once places in use after every job ended: %d, want 0 (a place was not given back)", n)
	}
}

// Slice 18, the setting off (today): the same queue and the same extra worker, and the graphics job still
// waits: no job takes a Files at once place, so nothing is handed back and the third processor job runs.
func TestGraphicsJobWaitsWhenTheSettingIsOff(t *testing.T) {
	s := newScenario(t)
	s.mixedQueue("p2.mkv", "p3.mkv", "graphics.mkv")
	c := &counter{}
	s.hang = c.hang(s)
	var size atomic.Int64
	size.Store(3)
	_, stop := startPool(t, s, &size, 0)
	defer stop()
	waitFor(t, func() bool { p, _ := c.counts(); return p == 3 }, "three processor jobs converting")
	if _, g := c.counts(); g != 0 {
		t.Errorf("a graphics encode started (%d), want none: with the setting off every file counts toward Files at once", g)
	}
	if n := FilePlacesInUse(); n != 0 {
		t.Errorf("Files at once places in use with the setting off: %d, want 0", n)
	}
}

// Slice 18: a graphics job whose card encode fails waits for a Files at once place before the processor
// converts it, so it never runs beside two processor jobs (a third processor encode). It writes that it waits.
func TestGraphicsFallbackWaitsForAFilesAtOncePlace(t *testing.T) {
	s := newScenario(t)
	ids := s.mixedQueue("p2.mkv", "graphics.mkv")
	s.failEncoder = "hevc_nvenc" // its graphics encode fails: the processor takes over
	var mu sync.Mutex
	processor := 0 // counted in the block callback: a blocked call never reaches the fake's own call log
	s.hang = func(name string, args []string) bool {
		if name != "ffmpeg" || !s.has(args, "libx265") {
			return false
		}
		mu.Lock()
		processor++
		mu.Unlock()
		return true
	}
	var size atomic.Int64
	size.Store(2)
	_, stop := startPool(t, s, &size, 2)
	defer stop()
	waitFor(t, func() bool {
		// the two processor jobs hold both places
		filePlaces.Lock()
		defer filePlaces.Unlock()
		return filePlaces.running == 2
	}, "the two processor jobs holding the places")
	size.Store(3) // the worker added for the card
	waitForLog(t, s, ids[2], "waiting for a Files at once place (2 of 2 in use)")
	mu.Lock()
	n := processor
	mu.Unlock()
	if n != 2 {
		t.Errorf("processor encodes %d, want 2: the fall-back waits for a place instead of being a third", n)
	}
	if j, _ := s.store.Get(ids[2]); j.Status != jobs.Running {
		t.Errorf("the graphics job is %s, want running (waiting for its place)", j.Status)
	}
}
