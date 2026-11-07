// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
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

// claimGap is the store with a Claim that lets f run in the gap right after the claim (the job is
// running in the database) and before RunOne returns from it.
type claimGap struct {
	*jobs.Store
	f func(jobs.Job)
}

func (q claimGap) Claim(worker string) (jobs.Job, error) {
	j, err := q.Store.Claim(worker)
	if err == nil && q.f != nil {
		q.f(j)
	}
	return j, err
}

// stopFromThePage is what POST /api/jobs/{id}/stop does: record the stop, then stop the worker.
func stopFromThePage(st *jobs.Store, w *Worker, id int64) {
	if running, err := st.RequestStop(id); err == nil && running {
		w.StopFile(id)
	}
}

// Stop this file landing between the claim and the worker registering the job: it is not lost. The
// job ends stopped by the person, nothing is replaced (retention 0: a replace would be final).
func TestStopInTheClaimGapStopsTheJob(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace) // no KeepDays: retention 0
	stopped := make(chan struct{})
	w.Queue = claimGap{s.store, func(j jobs.Job) {
		go func() { stopFromThePage(s.store, w, j.ID); close(stopped) }()
		time.Sleep(50 * time.Millisecond) // the stop runs now, in the gap
	}}
	w.RunOne(context.Background())
	<-stopped
	if _, err := s.store.Get(s.id); err == nil {
		t.Errorf("the job should be gone (stopped): %+v", s.job())
	}
	if f, _ := s.store.GetFile(s.src); f == nil || f.Reason != jobs.UserStoppedReason {
		t.Errorf("file record %+v", f)
	}
	s.assertOriginalSafe()
}

// A stop recorded on the job that reached no worker (or came late) is seen at the last check before
// the swap: nothing is replaced, the job ends stopped. Retention 0.
func TestStopRecordedBeforeTheSwapPreventsIt(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	s.afterChecks = func() { // the picture comparison, the last check
		if running, err := s.store.RequestStop(s.id); err != nil || !running {
			t.Errorf("RequestStop: %v %v", running, err)
		}
	}
	w.RunOne(context.Background())
	if _, err := s.store.Get(s.id); err == nil {
		t.Errorf("the job should be gone (stopped): %+v", s.job())
	}
	if f, _ := s.store.GetFile(s.src); f == nil || f.Reason != jobs.UserStoppedReason {
		t.Errorf("file record %+v", f)
	}
	s.assertOriginalSafe()
}

// A stop for a running job never deletes its record: only the worker ends it.
func TestRequestStopOnARunningJobOnlyMarksIt(t *testing.T) {
	s := newScenario(t)
	j, err := s.store.Claim("other")
	if err != nil {
		t.Fatal(err)
	}
	if running, err := s.store.RequestStop(j.ID); err != nil || !running {
		t.Fatalf("%v %v", running, err)
	}
	if got := s.job(); got.Status != jobs.Running {
		t.Errorf("status %s", got.Status)
	}
	if req, _ := s.store.StopRequested(j.ID); !req {
		t.Error("the stop is not recorded")
	}
	s.store.Release(j.ID)
	if _, err := s.store.Claim("again"); err == nil {
		t.Error("a job whose stop was requested must not be claimed again")
	}
	// after a restart, such a job ends stopped
	s.store.RequeueRunning()
	if _, err := s.store.Get(j.ID); err == nil {
		t.Error("the job should have ended stopped at startup")
	}
}

// Pause all landing between the worker's pause check and its claim: the claim refuses (it checks the
// pause itself), so nothing starts.
func TestPauseBetweenTheCheckAndTheClaim(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	first := true
	w.Paused = func() bool {
		if first { // the page's Pause all, right after this check
			first = false
			s.store.SetPaused(true)
			go w.PauseNow()
			return false
		}
		return s.store.Paused()
	}
	if ran, _ := w.RunOne(context.Background()); ran {
		t.Error("no job may run")
	}
	if j := s.job(); j.Status != jobs.Queued {
		t.Errorf("status %s", j.Status)
	}
	if n := s.count("libx265"); n != 0 {
		t.Errorf("%d encodes started", n)
	}
}

// Pause landing after the claim, before converting starts (only the flag, no PauseNow yet): the
// worker checks again and puts the job back.
func TestPauseAfterTheClaimBeforeConverting(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Paused = s.store.Paused
	testHook = func(at string) {
		if at == "registered" {
			s.store.SetPaused(true)
		}
	}
	defer func() { testHook = nil }()
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Queued {
		t.Errorf("status %s", j.Status)
	}
	if n := s.count("libx265"); n != 0 {
		t.Errorf("%d encodes started", n)
	}
	s.assertOriginalSafe()
}

// The pool never has more conversions running than parallelJobs allows after it was lowered and
// raised again while its workers were busy: busy retiring workers are taken back, none added beside them.
func TestPoolDownThenUpWhileBusy(t *testing.T) {
	s := newScenario(t)
	s.moreSrcs = map[string]bool{}
	for i := 1; i <= 3; i++ {
		p := filepath.Join(s.dir, "movie"+string(rune('0'+i))+".mkv")
		os.WriteFile(p, []byte("original-bytes"), 0o644)
		s.moreSrcs[p] = true
		s.store.Enqueue(jobs.Job{Library: s.dir, Path: p, DurationSec: 100, SizeBefore: 14})
	}
	var mu sync.Mutex
	s.hang = func(name string, args []string) bool { return name == "ffmpeg" && s.has(args, "libx265") }
	var size atomic.Int64
	size.Store(4)
	made := 0
	p := &Pool{
		New: func(n int) *Worker {
			mu.Lock()
			made++
			mu.Unlock()
			w := s.worker(Replace)
			w.ID = "w" + string(rune('0'+n))
			w.Poll = 10 * time.Millisecond
			return w
		},
		Size:  func() int { return int(size.Load()) },
		Every: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { p.Run(ctx); close(stopped) }()
	eventually(t, func() bool { return p.Busy() == 4 }, "4 conversions running")
	size.Store(1)
	eventually(t, func() bool { return p.Running() == 1 }, "down to 1 (3 retiring, still busy)")
	size.Store(2)
	eventually(t, func() bool { return p.Running() == 2 }, "up to 2")
	for i := 0; i < 10; i++ { // several resizes
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		n := made
		mu.Unlock()
		if b := p.Busy(); b > 4 || n > 4 {
			t.Fatalf("busy %d, workers made %d: more than the 4 already converting", b, n)
		}
	}
	size.Store(99)
	eventually(t, func() bool { return p.Running() == MaxParallel }, "capped")
	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the pool did not stop")
	}
}

// A library removal reaching a job inside its swap changes nothing: the swap finishes and the job is done
// (DELETE waits for that before it answers).
func TestLibraryRemovalInsideTheSwapFinishesIt(t *testing.T) {
	s := newScenario(t)
	w := s.keeping(7)
	l, _ := s.store.AddLibrary("Movies", s.dir, rules.SliderTemplate(3), true)
	s.store.Drop(s.id) // the scenario's job again, now in that library
	s.id, _ = s.store.Enqueue(jobs.Job{Library: s.dir, LibraryID: l.ID, Path: s.src, DurationSec: 100, SizeBefore: 14})
	swapHook = func(at string) error {
		if at == "before swap" {
			w.RequeueJob(s.id)
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
