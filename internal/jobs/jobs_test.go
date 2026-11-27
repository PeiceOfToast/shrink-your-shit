// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

var allStates = []State{Waiting, RetryWait, Running, Done, Failed, Skipped, Cancelled}

// TestTransitionTable runs the guard itself for every (from, verb, to) edge against every state. An allowed pair
// moves the job; every other pair is refused with ErrChangedSince naming the state the job is really in.
func TestTransitionTable(t *testing.T) {
	for _, e := range transitions {
		for _, from := range allStates {
			allowed := false
			for _, f := range e.from {
				if f == from {
					allowed = true
				}
			}
			t.Run(fmt.Sprintf("%s/%s->%s/from-%s", e.verb, e.from, e.to, from), func(t *testing.T) {
				env := newEnv(t)
				env.addLibrary(t, 1, false)
				id := env.enqueue(t, 10, 1)
				env.setState(t, id, from)

				err := env.db.Tx(context.Background(), func(tx *sql.Tx) error {
					return move(tx, id, e.verb, e.to, change{})
				})
				if allowed {
					if err != nil {
						t.Fatalf("allowed pair refused: %v", err)
					}
					if got := env.stateOf(t, id); got != e.to {
						t.Fatalf("state = %s, want %s", got, e.to)
					}
					return
				}
				var ce *ErrChangedSince
				if !errors.As(err, &ce) {
					t.Fatalf("refused pair gave %v, want ErrChangedSince", err)
				}
				if ce.State != from {
					t.Fatalf("ErrChangedSince.State = %s, want %s", ce.State, from)
				}
				if got := env.stateOf(t, id); got != from {
					t.Fatalf("refused move changed state to %s", got)
				}
			})
		}
	}
}

// TestClaimIsAtomic: eight claimers race for one job; exactly one wins.
func TestClaimIsAtomic(t *testing.T) {
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	env.enqueue(t, 10, 1)

	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	errs := []error{}
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := env.q.Claim(context.Background(), fmt.Sprintf("w%d", i), ClaimOptions{})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if job != nil {
				won++
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("claim errors: %v", errs)
	}
	if won != 1 {
		t.Fatalf("%d claimers won, want exactly 1", won)
	}
	attempts, err := env.q.Attempts(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("%d attempt rows, want 1", len(attempts))
	}
}

// TestClaimOrder: top jumps the line; newest and oldest take the ends of the queue.
func TestClaimOrder(t *testing.T) {
	ctx := context.Background()
	t.Run("newest", func(t *testing.T) {
		env := newEnv(t)
		env.addLibrary(t, 1, false)
		first := env.enqueue(t, 1, 1)
		env.clk.advance(time.Minute)
		second := env.enqueue(t, 2, 1)
		job, err := env.q.Claim(ctx, "w", ClaimOptions{Order: OrderNewest})
		if err != nil || job == nil || job.ID != second {
			t.Fatalf("newest took %v (err %v), want %d; first was %d", job, err, second, first)
		}
	})
	t.Run("oldest", func(t *testing.T) {
		env := newEnv(t)
		env.addLibrary(t, 1, false)
		first := env.enqueue(t, 1, 1)
		env.clk.advance(time.Minute)
		env.enqueue(t, 2, 1)
		job, err := env.q.Claim(ctx, "w", ClaimOptions{Order: OrderOldest})
		if err != nil || job == nil || job.ID != first {
			t.Fatalf("oldest took %v (err %v), want %d", job, err, first)
		}
	})
	t.Run("top", func(t *testing.T) {
		env := newEnv(t)
		env.addLibrary(t, 1, false)
		first := env.enqueue(t, 1, 1)
		env.clk.advance(time.Minute)
		env.enqueue(t, 2, 1)
		env.exec(t, `UPDATE jobs SET top = 1 WHERE id = ?`, first)
		job, err := env.q.Claim(ctx, "w", ClaimOptions{Order: OrderNewest})
		if err != nil || job == nil || job.ID != first {
			t.Fatalf("top job not first: took %v (err %v), want %d", job, err, first)
		}
	})
	t.Run("size orders read files.size, not size_before", func(t *testing.T) {
		// File 1 is the bigger source (900) and file 2 the smaller (100), queued in that order. size_before
		// is set the other way round on the jobs, so a sort on size_before would pick the wrong job.
		for _, c := range []struct {
			o    Order
			want int // which job is first: 1 for the first queued, 2 for the second
		}{{OrderLargest, 1}, {OrderSmallest, 2}} {
			env := newEnv(t)
			env.addLibrary(t, 1, false)
			env.addFile(t, 1, 1, 900)
			env.addFile(t, 2, 1, 100)
			first := env.enqueue(t, 1, 1)
			env.clk.advance(time.Minute)
			second := env.enqueue(t, 2, 1)
			env.exec(t, `UPDATE jobs SET size_before = 1 WHERE id = ?`, first)
			env.exec(t, `UPDATE jobs SET size_before = 999 WHERE id = ?`, second)
			want := first
			if c.want == 2 {
				want = second
			}
			job, err := env.q.Claim(ctx, "w", ClaimOptions{Order: c.o})
			if err != nil || job == nil || job.ID != want {
				t.Fatalf("%s took %v (err %v), want %d", c.o, job, err, want)
			}
		}
	})
	t.Run("unknown order refused", func(t *testing.T) {
		env := newEnv(t)
		if _, err := env.q.Claim(ctx, "w", ClaimOptions{Order: "random"}); err == nil {
			t.Fatal("unknown order accepted")
		}
	})
}

// TestClaimSkipsExceptPausedAndFutureRetry covers the three things a claim must not take.
func TestClaimSkipsExceptPausedAndFutureRetry(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	a := env.enqueue(t, 1, 1)
	b := env.enqueue(t, 2, 1)

	job, err := env.q.Claim(ctx, "w", ClaimOptions{Except: []int64{a}})
	if err != nil || job == nil || job.ID != b {
		t.Fatalf("except: took %v (err %v), want %d", job, err, b)
	}
	if err := env.q.Requeue(ctx, b, "reset for next step"); err != nil {
		t.Fatal(err)
	}

	env.setPaused(t, 1, true)
	job, err = env.q.Claim(ctx, "w", ClaimOptions{})
	if err != nil || job != nil {
		t.Fatalf("paused library: took %v (err %v), want nothing", job, err)
	}
	env.setPaused(t, 1, false)

	env.exec(t, `UPDATE jobs SET state = 'retry_wait', next_try_at = ? WHERE id = ?`, ms(env.q.now())+5*60_000, a)
	job, err = env.q.Claim(ctx, "w", ClaimOptions{Except: []int64{b}})
	if err != nil || job != nil {
		t.Fatalf("future next_try_at: took %v (err %v), want nothing", job, err)
	}
	env.clk.advance(6 * time.Minute)
	job, err = env.q.Claim(ctx, "w", ClaimOptions{Except: []int64{b}})
	if err != nil || job == nil || job.ID != a {
		t.Fatalf("past next_try_at: took %v (err %v), want %d", job, err, a)
	}
}

func TestClaimLibraryFilter(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	env.addLibrary(t, 2, false)
	env.enqueue(t, 1, 1)
	b := env.enqueue(t, 2, 2)

	job, err := env.q.Claim(ctx, "w", ClaimOptions{Libraries: []int64{2}})
	if err != nil || job == nil || job.ID != b {
		t.Fatalf("library filter took %v (err %v), want %d", job, err, b)
	}
	job, err = env.q.Claim(ctx, "w", ClaimOptions{Libraries: []int64{}})
	if err != nil || job != nil {
		t.Fatalf("empty library list took %v (err %v), want nothing", job, err)
	}
}

// TestEnqueueAlreadyOpen: a second enqueue of an open file returns the same job and leaves one row.
func TestEnqueueAlreadyOpen(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	first := env.enqueue(t, 1, 1)
	id, open, err := env.q.Enqueue(ctx, 1, 1, 1, "/media/a.mkv", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !open || id != first {
		t.Fatalf("second enqueue = (%d, %v), want (%d, true)", id, open, first)
	}
	if n := env.countJobs(t); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
}

// TestEnqueueRefusesPausedOrMissingLibrary: no row for a library that is paused or does not exist.
func TestEnqueueRefusesPausedOrMissingLibrary(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 3, true)
	for _, lib := range []int64{3, 99} { // 3 is paused, 99 is not there at all
		_, _, err := env.q.Enqueue(ctx, 50+lib, lib, 1, "/m/a.mkv", "test")
		if !errors.Is(err, ErrLibraryUnavailable) {
			t.Fatalf("library %d: err = %v, want ErrLibraryUnavailable", lib, err)
		}
	}
	if n := env.countJobs(t); n != 0 {
		t.Fatalf("%d rows, want 0", n)
	}
}

// TestRetryBackoffThenFailed: a retry waits 5 minutes, then 30, then the third failure is failed.
func TestRetryBackoffThenFailed(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	retry := Result{Verdict: VerdictRetry, Reason: "encoder fell over"}

	for try, wait := range []time.Duration{5 * time.Minute, 30 * time.Minute} {
		job := mustClaim(t, env, id)
		if err := env.q.Finish(ctx, id, job.Attempt, retry); err != nil {
			t.Fatalf("try %d: %v", try+1, err)
		}
		got, _ := env.q.Get(ctx, id)
		if got.State != RetryWait || got.Tries != try+1 {
			t.Fatalf("after try %d: state %s tries %d, want retry_wait %d", try+1, got.State, got.Tries, try+1)
		}
		if got.NextTryAt == nil || got.NextTryAt.Sub(env.clk.now()) != wait {
			t.Fatalf("next_try_at = %v, want now+%s", got.NextTryAt, wait)
		}
		if again, _ := env.q.Claim(ctx, "w", ClaimOptions{}); again != nil {
			t.Fatalf("job claimable before its wait ended")
		}
		env.clk.advance(wait)
	}

	job := mustClaim(t, env, id)
	if job.Attempt != 3 {
		t.Fatalf("third attempt = %d", job.Attempt)
	}
	if err := env.q.Finish(ctx, id, job.Attempt, retry); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.State != Failed || got.Tries != 3 {
		t.Fatalf("after third failure: state %s tries %d, want failed 3", got.State, got.Tries)
	}
	atts, _ := env.q.Attempts(ctx, id)
	if len(atts) != 3 {
		t.Fatalf("%d attempt rows, want 3", len(atts))
	}
}

// TestRetryReusesRowAndNextClaimIsAttemptTwo: Retry puts the same row back; the next claim is attempt 2.
func TestRetryReusesRowAndNextClaimIsAttemptTwo(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictFail, Reason: "bad input", FailedStep: "encode"}); err != nil {
		t.Fatal(err)
	}
	if err := env.q.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.ID != id || got.State != Waiting || got.Tries != 0 {
		t.Fatalf("after retry: id %d state %s tries %d", got.ID, got.State, got.Tries)
	}
	if n := env.countJobs(t); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
	job = mustClaim(t, env, id)
	if job.Attempt != 2 {
		t.Fatalf("attempt after retry = %d, want 2", job.Attempt)
	}
}

// TestSkipIsNeverAFailure (L-Q-9): a skip is skipped, not failed, uses no try, and is recorded as a skip.
func TestSkipIsNeverAFailure(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictSkip, SkipReason: "already_smaller", Reason: "already small enough"}); err != nil {
		t.Fatal(err)
	}

	got, _ := env.q.Get(ctx, id)
	if got.State != Skipped || got.Tries != 0 || got.SkipReason != "already_smaller" {
		t.Fatalf("skip: state %s tries %d skip %q", got.State, got.Tries, got.SkipReason)
	}
	failed, _ := env.q.List(ctx, ListFilter{Shown: "failed"})
	if len(failed) != 0 {
		t.Fatalf("skip listed as failed: %d rows", len(failed))
	}
	skipped, _ := env.q.List(ctx, ListFilter{Shown: "skipped"})
	if len(skipped) != 1 {
		t.Fatalf("skip not listed as skipped: %d rows", len(skipped))
	}
	atts, _ := env.q.Attempts(ctx, id)
	if len(atts) != 1 || atts[0].Outcome != "skip" {
		t.Fatalf("attempt outcome = %+v, want skip", atts)
	}
}

// TestSkipDoesNotRetry: a skipped job cannot be retried as if it had failed.
func TestSkipDoesNotRetry(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictSkip, SkipReason: "x", Reason: "y"}); err != nil {
		t.Fatal(err)
	}
	err := env.q.Retry(ctx, id)
	var ce *ErrChangedSince
	if !errors.As(err, &ce) || ce.State != Skipped {
		t.Fatalf("retry of skip = %v, want ErrChangedSince in skipped", err)
	}
}

// TestSkipWritesOnlyTheSkipFields: a skip leaves no failure reason on the head row.
func TestSkipWritesOnlyTheSkipFields(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictSkip, SkipReason: "x", Reason: "y"}); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.FailedStep != "" {
		t.Fatalf("skip set failed_step %q", got.FailedStep)
	}
}

// TestRecoverRequeuesWithoutATry: a restart moves running back to waiting, calls the sweep, emits requeued.
func TestRecoverRequeuesWithoutATry(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	mustClaim(t, env, id)

	ch, stop := env.bus.Subscribe(events.Filter{JobID: id, Kind: events.KindRequeued})
	defer stop()

	var swept []int64
	err := env.q.Recover(ctx, func(_ context.Context, j Job) error {
		swept = append(swept, j.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0] != id {
		t.Fatalf("sweep called for %v, want [%d]", swept, id)
	}
	got, _ := env.q.Get(ctx, id)
	if got.State != Waiting || got.Tries != 0 {
		t.Fatalf("after recover: state %s tries %d", got.State, got.Tries)
	}
	select {
	case e := <-ch:
		if e.Kind != events.KindRequeued || e.Source != "jobs" || e.JobID != id {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no requeued event")
	}
}

// TestRequeueDoesNotCountATry (L-Q-5).
func TestRequeueDoesNotCountATry(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	mustClaim(t, env, id)
	if err := env.q.Requeue(ctx, id, "worker went away"); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.State != Waiting || got.Tries != 0 {
		t.Fatalf("requeue: state %s tries %d", got.State, got.Tries)
	}
	atts, _ := env.q.Attempts(ctx, id)
	if len(atts) != 1 || atts[0].Outcome != "requeued" {
		t.Fatalf("attempt after requeue = %+v", atts)
	}
}

// TestFinishTwiceRefused: the attempt row is written once and never rewritten.
func TestFinishTwiceRefused(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	done := Result{Verdict: VerdictDone, Outcome: "smaller", SizeBefore: 100, SizeAfter: 40, OutputPath: "/out/a.mkv"}
	if err := env.q.Finish(ctx, id, job.Attempt, done); err != nil {
		t.Fatal(err)
	}
	err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictFail, Reason: "late"})
	var ce *ErrChangedSince
	if !errors.As(err, &ce) || ce.State != Done {
		t.Fatalf("second finish = %v, want ErrChangedSince in done", err)
	}
	atts, _ := env.q.Attempts(ctx, id)
	if len(atts) != 1 || atts[0].Outcome != "smaller" || atts[0].Failure != "" {
		t.Fatalf("attempt rewritten: %+v", atts)
	}
}

// TestFinishRefusesStaleAttempt: a worker that finishes an attempt the job no longer has is refused.
func TestFinishRefusesStaleAttempt(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	mustClaim(t, env, id)
	err := env.q.Finish(ctx, id, 7, Result{Verdict: VerdictDone, Outcome: "smaller"})
	var ce *ErrChangedSince
	if !errors.As(err, &ce) {
		t.Fatalf("stale attempt = %v, want ErrChangedSince", err)
	}
	if got := env.stateOf(t, id); got != Running {
		t.Fatalf("stale finish changed state to %s", got)
	}
}

// TestCancelRunningReturnsWorker: cancelling a running job tells the caller which worker to stop.
func TestCancelRunningReturnsWorker(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	if _, err := env.q.Claim(ctx, "worker-7", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := env.q.Cancel(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WasRunning || res.WorkerID != "worker-7" || res.Attempt != 1 {
		t.Fatalf("cancel result = %+v", res)
	}
	if got := env.stateOf(t, id); got != Cancelled {
		t.Fatalf("state = %s", got)
	}
	all, _ := env.q.List(ctx, ListFilter{})
	if len(all) != 0 {
		t.Fatalf("cancelled job listed: %d rows", len(all))
	}
}

// TestRetryRefusedWhileFileOpen: a failed job cannot come back while its file has a new open job.
func TestRetryRefusedWhileFileOpen(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictFail, Reason: "bad"}); err != nil {
		t.Fatal(err)
	}
	other := env.enqueue(t, 1, 1)
	err := env.q.Retry(ctx, id)
	var busy *ErrFileBusy
	if !errors.As(err, &busy) || busy.JobID != other {
		t.Fatalf("retry = %v, want ErrFileBusy for %d", err, other)
	}
}

// TestListFilters: retry_wait reads as waiting; cancelled is never listed.
func TestListFilters(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	a := env.enqueue(t, 1, 1)
	env.setState(t, a, RetryWait)
	env.enqueue(t, 2, 1)
	waiting, err := env.q.List(ctx, ListFilter{Shown: "waiting"})
	if err != nil || len(waiting) != 2 {
		t.Fatalf("waiting = %d rows (err %v), want 2", len(waiting), err)
	}
	if _, err := env.q.List(ctx, ListFilter{Shown: "cancelled"}); err == nil {
		t.Fatal("cancelled accepted as a list state")
	}
	hits, _ := env.q.List(ctx, ListFilter{PathSearch: "/MEDIA/2.MKV"}) // file 2 is /media/2.mkv; the search ignores case
	if len(hits) != 1 {
		t.Fatalf("path search = %d rows, want 1", len(hits))
	}
}

// mustClaim claims id, failing the test unless that exact job is the one taken.
func mustClaim(t *testing.T, env *testEnv, id int64) Job {
	t.Helper()
	job, err := env.q.Claim(context.Background(), "w", ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != id {
		t.Fatalf("claim gave %v, want job %d", job, id)
	}
	return *job
}
