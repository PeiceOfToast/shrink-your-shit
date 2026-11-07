// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// runHung starts RunOne with the encode hanging (no progress) and returns once the encode has begun,
// with a channel closed when RunOne returns.
func runHung(t *testing.T, s *scenario, w *Worker) chan struct{} {
	t.Helper()
	var encoding atomic.Bool
	s.hang = func(name string, args []string) bool {
		if name == "ffmpeg" && s.has(args, "libx265") {
			encoding.Store(true)
			return true
		}
		return false
	}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	for i := 0; !encoding.Load(); i++ {
		if i > 500 {
			t.Fatal("the encode never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return done
}

func waitDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the job did not stop")
	}
}

// Pause all: the running conversion stops, the original is untouched, nothing is left behind, and the
// job is back in the queue; a paused worker takes no job.
func TestPauseRequeuesTheRunningJob(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	done := runHung(t, s, w)
	w.PauseNow()
	waitDone(t, done)
	if j := s.job(); j.Status != jobs.Queued {
		t.Errorf("status %s", j.Status)
	}
	s.assertOriginalSafe()
	w.Paused = func() bool { return true }
	if ran, _ := w.RunOne(context.Background()); ran || s.job().Status != jobs.Queued {
		t.Error("a paused worker takes no job")
	}
}

// Stop this file: the running conversion stops, the original is untouched, the job goes and the file
// is recorded as stopped by the person (not converted again until Convert again).
func TestStopFileRecordsUserStopped(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	done := runHung(t, s, w)
	if !w.StopFile(s.id) {
		t.Fatal("the worker runs it")
	}
	waitDone(t, done)
	if _, err := s.store.Get(s.id); err == nil {
		t.Error("the job record goes")
	}
	if f, _ := s.store.GetFile(s.src); f == nil || f.Reason != jobs.UserStoppedReason {
		t.Errorf("file record %+v", f)
	}
	s.assertOriginalSafe()
	if w.StopFile(s.id) {
		t.Error("nothing runs now")
	}
}

// The watchdog: an encode that reports no progress for StallAfter is stopped and fails with a plain
// reason; the original is untouched.
func TestStallWatchdogFailsAStuckEncode(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.StallAfter = 300 * time.Millisecond
	done := runHung(t, s, w)
	waitDone(t, done)
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") || !strings.Contains(j.Error, "original is untouched") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// A stop that arrives inside the swap window does not interrupt the swap: the job ends done, the new
// file in place and the original kept (no half swap).
func TestStopInTheSwapWindowEndsDone(t *testing.T) {
	s := newScenario(t)
	w := s.keeping(7)
	swapHook = func(at string) error {
		if at == "before swap" {
			if !w.StopFile(s.id) {
				t.Error("the job should be running")
			}
			w.PauseNow() // and a Pause all on top: neither interrupts the swap
		}
		return nil
	}
	defer func() { swapHook = nil }()
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("status %s %q", j.Status, j.Error)
	}
	if read(s.src) != "new" || read(s.trash()) != "original-bytes" {
		t.Errorf("at the name %q, kept %q", read(s.src), read(s.trash()))
	}
}

// The pool follows parallelJobs: up and down, without a restart.
func TestPoolFollowsParallelJobs(t *testing.T) {
	s := newScenario(t)
	var size atomic.Int64
	size.Store(2)
	var seen sync.Map
	p := &Pool{
		New: func(n int) *Worker {
			w := s.worker(Replace)
			w.ID = "w" + string(rune('0'+n))
			w.Poll = 10 * time.Millisecond
			w.Paused = func() bool { seen.Store(w.ID, true); return true } // polls without taking work
			return w
		},
		Size:  func() int { return int(size.Load()) },
		Every: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { p.Run(ctx); close(stopped) }()
	eventually(t, func() bool { return p.Running() == 2 }, "2 workers")
	eventually(t, func() bool { _, a := seen.Load("w1"); _, b := seen.Load("w2"); return a && b }, "both polling")
	size.Store(1)
	eventually(t, func() bool { return p.Running() == 1 }, "down to 1")
	size.Store(3)
	eventually(t, func() bool { return p.Running() == 3 }, "up to 3")
	size.Store(99)
	eventually(t, func() bool { return p.Running() == MaxParallel }, "capped")
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the pool did not stop")
	}
}

func eventually(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}
