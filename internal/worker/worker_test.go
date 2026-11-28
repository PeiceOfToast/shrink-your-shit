// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// fakeSource hands out queued work and records what the worker wrote back.
type fakeSource struct {
	mu        sync.Mutex
	queue     []Work
	claims    int
	finished  []jobs.Result
	finishIDs []int64
	requeued  []string
	cancelled []int64
	// running is what Running answers; nil means the job is still running.
	running *bool
}

func (f *fakeSource) Running(ctx context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running == nil {
		return true, nil
	}
	return *f.running, nil
}

func (f *fakeSource) Claim(ctx context.Context, workerID string) (*Work, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if len(f.queue) == 0 {
		return nil, nil
	}
	w := f.queue[0]
	f.queue = f.queue[1:]
	return &w, nil
}

func (f *fakeSource) Finish(ctx context.Context, id int64, attempt int, r jobs.Result) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishIDs = append(f.finishIDs, id)
	f.finished = append(f.finished, r)
	return nil
}

func (f *fakeSource) Requeue(ctx context.Context, id int64, why string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requeued = append(f.requeued, why)
	return nil
}

func (f *fakeSource) Cancel(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return nil
}

func (f *fakeSource) snapshot() (finished []jobs.Result, requeued []string, cancelled []int64, claims int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jobs.Result(nil), f.finished...), append([]string(nil), f.requeued...),
		append([]int64(nil), f.cancelled...), f.claims
}

func (f *fakeSource) push(w Work) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, w)
}

// recorder keeps every event the worker emits.
type recorder struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recorder) Emit(e events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.events...)
}

func job(id int64) Work {
	return Work{Job: jobs.Job{ID: id, Attempt: 1}, In: flow.RunInput{JobID: id, Attempt: 1}}
}

// waitFor polls until cond holds or the test has waited two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitForWithin(t, 2*time.Second, what, cond)
}

// waitForWithin is waitFor with its own limit, for tests that run a real encode: a loaded CI runner can take
// longer than two seconds for one (#858's go job timed out at 2.46 s on a 1.9 s encode).
func waitForWithin(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// start runs a worker in the background and returns a stop function that waits for it to return.
func start(t *testing.T, w *Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not return after shutdown")
		}
	}
}

func TestSettleMapsEveryOutcome(t *testing.T) {
	cases := []struct {
		name     string
		res      flow.Result
		stopping bool
		want     jobs.Verdict
		outcome  string
		end      End
	}{
		{"smaller", flow.Result{Outcome: flow.OutcomeSmaller, Message: "ok"}, false, jobs.VerdictDone, "smaller", EndFinish},
		{"bigger kept", flow.Result{Outcome: flow.OutcomeBiggerKept, Message: "kept"}, false, jobs.VerdictDone, "bigger_kept", EndFinish},
		{"skip", flow.Result{Outcome: flow.OutcomeSkip, Reason: "already_hevc"}, false, jobs.VerdictSkip, "", EndFinish},
		{"may pass", flow.Result{Outcome: flow.OutcomeMayPass, Reason: "check.unavailable"}, false, jobs.VerdictRetry, "", EndFinish},
		{"fatal", flow.Result{Outcome: flow.OutcomeFatal, Reason: "encode.failed"}, false, jobs.VerdictFail, "", EndFinish},
		{"requeue is not a try", flow.Result{Outcome: flow.OutcomeRequeue, Reason: "file.still_changing"}, false, "", "", EndRequeue},
		{"cancelled by a person", flow.Result{Outcome: flow.OutcomeCancelled}, false, "", "", EndCancel},
		{"cancelled by shutdown", flow.Result{Outcome: flow.OutcomeCancelled}, true, "", "", EndRequeue},
		{"empty outcome fails", flow.Result{}, false, jobs.VerdictFail, "", EndFinish},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, end := settle(c.res, c.stopping)
			if end != c.end {
				t.Fatalf("end = %v, want %v", end, c.end)
			}
			if end != EndFinish {
				return
			}
			if got.Verdict != c.want || got.Outcome != c.outcome {
				t.Fatalf("result = %+v, want verdict %q outcome %q", got, c.want, c.outcome)
			}
		})
	}
}

func TestPanicFailsTheJobAndTheSlotCarriesOn(t *testing.T) {
	src := &fakeSource{queue: []Work{job(1), job(2)}}
	bus := &recorder{}
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		if in.JobID == 1 {
			panic("boom")
		}
		return flow.Result{Outcome: flow.OutcomeSmaller, SizeBefore: 10, SizeAfter: 4}
	}
	w := New(Config{Source: src, Run: run, Bus: bus, Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()

	waitFor(t, "two results", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == 2
	})
	f, _, _, _ := src.snapshot()
	if f[0].Verdict != jobs.VerdictFail || f[0].Reason != "internal_error" {
		t.Errorf("panicking job = %+v, want fail with internal_error", f[0])
	}
	if f[1].Verdict != jobs.VerdictDone || f[1].Outcome != "smaller" {
		t.Errorf("next job = %+v, want done/smaller", f[1])
	}
	var sawStack bool
	for _, e := range bus.all() {
		if e.Code == "internal_error" && e.Level == events.LevelError &&
			strings.Contains(e.Message, "panic in run: boom") && strings.Contains(e.Message, "goroutine ") {
			sawStack = true
		}
	}
	if !sawStack {
		t.Errorf("no error event with the panic and its stack: %+v", bus.all())
	}
}

func TestAttemptLogGoesToItsOwnFile(t *testing.T) {
	src := &fakeSource{queue: []Work{job(7)}}
	dir := t.TempDir()
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		_, _ = env.Raw.Write([]byte("ffmpeg says hi\n"))
		return flow.Result{Outcome: flow.OutcomeSmaller}
	}
	w := New(Config{Source: src, Run: run, DataDir: dir, Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()
	waitFor(t, "a result", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == 1
	})
	b, err := os.ReadFile(filepath.Join(dir, "jobs", "7", "1.log"))
	if err != nil || string(b) != "ffmpeg says hi\n" {
		t.Fatalf("log = %q, %v; want the run's output in jobs/7/1.log", b, err)
	}
}

func TestShutdownPastGraceRequeuesWithoutATry(t *testing.T) {
	src := &fakeSource{queue: []Work{job(3)}}
	started := make(chan struct{})
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		close(started)
		<-ctx.Done() // a run that ignores shutdown until it is cancelled
		return flow.Result{Outcome: flow.OutcomeCancelled}
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := New(Config{Source: src, Run: run, ShutdownGrace: 20 * time.Millisecond, Tick: time.Hour})
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not return after the grace period")
	}

	finished, requeued, cancelled, _ := src.snapshot()
	if len(finished) != 0 || len(cancelled) != 0 {
		t.Errorf("finished %v, cancelled %v; want neither", finished, cancelled)
	}
	if len(requeued) != 1 || requeued[0] != "shutdown" {
		t.Errorf("requeued = %v; want one requeue for shutdown", requeued)
	}
}

// TestCancelJobStopsTheRunningRun: the api's cancel ends the run's context, the run reports cancelled, and the queue
// records it (S3-6a). The stop is looked up by job id while the job runs.
func TestCancelJobStopsTheRunningRun(t *testing.T) {
	src := &fakeSource{queue: []Work{job(4)}}
	started := make(chan struct{})
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		close(started)
		<-ctx.Done()
		return flow.Result{Outcome: flow.OutcomeCancelled}
	}
	w := New(Config{Source: src, Run: run, Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()

	<-started
	if !w.CancelJob(4) {
		t.Fatal("CancelJob did not find the running job")
	}
	waitFor(t, "the cancelled job to be recorded", func() bool {
		_, _, cancelled, _ := src.snapshot()
		return len(cancelled) == 1 && cancelled[0] == 4
	})
	f, _, _, _ := src.snapshot()
	if len(f) != 0 {
		t.Fatalf("finished = %v; a cancelled run must not write a result", f)
	}
	if w.CancelJob(4) {
		t.Fatal("CancelJob still found the job after its run ended")
	}
}

// TestCancelBeforeTheRunStartsStopsIt: a cancel between the claim and the run leaves the row cancelled, so the worker
// asks the queue before it runs and stops at once rather than encoding a job the person cancelled (L-Q-7).
func TestCancelBeforeTheRunStartsStopsIt(t *testing.T) {
	notRunning := false
	src := &fakeSource{queue: []Work{job(5)}, running: &notRunning}
	entered := make(chan struct{})
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		close(entered)
		if ctx.Err() == nil {
			return flow.Result{Outcome: flow.OutcomeSmaller}
		}
		return flow.Result{Outcome: flow.OutcomeCancelled}
	}
	w := New(Config{Source: src, Run: run, Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()

	waitFor(t, "the cancelled row to be recorded", func() bool {
		_, _, cancelled, _ := src.snapshot()
		return len(cancelled) == 1 && cancelled[0] == 5
	})
	<-entered
	f, _, _, _ := src.snapshot()
	if len(f) != 0 {
		t.Fatalf("finished = %v; the run was already cancelled by the row", f)
	}
}

func TestIdleSlotWakesOnEnqueueWithoutSpinning(t *testing.T) {
	src := &fakeSource{}
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		return flow.Result{Outcome: flow.OutcomeSmaller}
	}
	// The tick is an hour: only a wake can get the job run inside the test's time.
	w := New(Config{Source: src, Run: run, Tick: time.Hour})
	stop := start(t, w)
	defer stop()

	time.Sleep(50 * time.Millisecond)
	_, _, _, idleClaims := src.snapshot()
	if idleClaims > 3 {
		t.Fatalf("idle slot claimed %d times in 50 ms; want it parked", idleClaims)
	}

	src.push(job(9))
	w.Wake()
	start2 := time.Now()
	waitFor(t, "the woken job to finish", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == 1
	})
	if d := time.Since(start2); d > 500*time.Millisecond {
		t.Fatalf("woken job took %v; want well under the tick", d)
	}

	time.Sleep(50 * time.Millisecond)
	_, _, _, after := src.snapshot()
	if after-idleClaims > 5 {
		t.Fatalf("claims rose from %d to %d with no work; the slot is spinning", idleClaims, after)
	}
}

// Every slot claims its own job: with work for three, one Wake starts all three at once instead of one slot
// draining the queue while the others stay parked.
func TestFilesAtOnceRunThatManyJobsAtOnce(t *testing.T) {
	const n = 3
	src := &fakeSource{}
	started := make(chan int64, n)
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }

	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		started <- in.JobID
		<-release
		return flow.Result{Outcome: flow.OutcomeSmaller}
	}
	// The tick is an hour: only a wake can start the jobs inside the test's time, and a slot that misses the
	// wake would fail here rather than in the field.
	w := New(Config{Slots: n, Source: src, Run: run, Tick: time.Hour})
	stop := start(t, w)
	defer stop()
	defer letGo() // before stop: a failure must not leave a job waiting on release

	// Every slot makes its first, empty claim, so they are all parked when the work arrives: a slot still on its
	// first claim would take a job without any wake and hide a wake that reached one slot only.
	waitFor(t, "every slot to park", func() bool {
		_, _, _, claims := src.snapshot()
		return claims >= n
	})
	for id := int64(1); id <= n; id++ {
		src.push(job(id))
	}
	w.Wake()
	seen := map[int64]bool{}
	for len(seen) < n {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("%d of %d jobs started; a wake reached one slot only", len(seen), n)
		}
	}
	letGo()
	waitFor(t, "every job to finish", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == n
	})
}

// Outside its hours the worker takes no new job; a job already running still finishes, and the next look
// after the hours open picks up the work waiting (page 10's "a running job finishes").
func TestHoursHoldNewJobsAndLetRunningOnesFinish(t *testing.T) {
	src := &fakeSource{}
	var open atomic.Bool
	started := make(chan int64, 2)
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		started <- in.JobID
		<-release
		return flow.Result{Outcome: flow.OutcomeSmaller}
	}
	w := New(Config{Source: src, Run: run, Tick: 5 * time.Millisecond, MayClaim: open.Load})
	stop := start(t, w)
	defer stop()
	defer letGo()

	src.push(job(1))
	src.push(job(2))
	time.Sleep(50 * time.Millisecond)
	if _, _, _, claims := src.snapshot(); claims != 0 {
		t.Fatalf("claimed %d jobs outside the hours, want none", claims)
	}

	// The hours open: the waiting file starts. The hours closing again does not stop it.
	open.Store(true)
	w.Wake()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the file did not start once the hours opened")
	}
	open.Store(false)
	letGo()
	waitFor(t, "the running file to finish", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == 1
	})
	f, requeued, cancelled, _ := src.snapshot()
	if f[0].Verdict != jobs.VerdictDone {
		t.Errorf("result = %+v, want the running file done", f[0])
	}
	if len(requeued) != 0 || len(cancelled) != 0 {
		t.Errorf("requeued %v, cancelled %v; closing the hours must not stop a running file", requeued, cancelled)
	}
	time.Sleep(50 * time.Millisecond)
	if _, _, _, claims := src.snapshot(); claims != 1 {
		t.Errorf("claimed %d jobs, want 1: the second file waits outside the hours", claims)
	}
}

func TestClaimErrorBacksOffAndKeepsRunning(t *testing.T) {
	errSrc := &erroringSource{fakeSource: &fakeSource{queue: []Work{job(5)}}, failures: 2}
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		return flow.Result{Outcome: flow.OutcomeSmaller}
	}
	w := New(Config{Source: errSrc, Run: run, Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()
	waitFor(t, "the job after two claim errors", func() bool {
		f, _, _, _ := errSrc.snapshot()
		return len(f) == 1
	})
}

// erroringSource fails its first claims, then behaves like the fake.
type erroringSource struct {
	*fakeSource
	failures int
}

func (e *erroringSource) Claim(ctx context.Context, workerID string) (*Work, error) {
	e.fakeSource.mu.Lock()
	if e.failures > 0 {
		e.failures--
		e.fakeSource.mu.Unlock()
		return nil, errors.New("database is locked")
	}
	e.fakeSource.mu.Unlock()
	return e.fakeSource.Claim(ctx, workerID)
}
