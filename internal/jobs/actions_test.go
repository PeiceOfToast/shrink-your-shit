// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"errors"
	"testing"
)

// TestManualRetryClearsThePinnedRevision: a person pressing Retry runs the workflow as published now, so the retry
// clears the revision the job was queued with (lead, 11-28). An automatic retry keeps it: the job continues the run
// it started.
func TestManualRetryClearsThePinnedRevision(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)

	manual := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, manual)
	if err := env.q.Finish(ctx, manual, job.Attempt, Result{Verdict: VerdictFail, Reason: "bad input"}); err != nil {
		t.Fatal(err)
	}
	if err := env.q.Retry(ctx, manual); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, manual)
	if got.WorkflowRev != 0 {
		t.Fatalf("manual retry kept revision %d; want the published one", got.WorkflowRev)
	}

	auto := env.enqueue(t, 2, 1)
	job = mustClaim(t, env, auto)
	if err := env.q.Finish(ctx, auto, job.Attempt, Result{Verdict: VerdictRetry, Reason: "encoder fell over"}); err != nil {
		t.Fatal(err)
	}
	got, _ = env.q.Get(ctx, auto)
	if got.State != RetryWait || got.WorkflowRev != 1 {
		t.Fatalf("automatic retry: state %s revision %d; want retry_wait 1", got.State, got.WorkflowRev)
	}
}

// TestTopPutsAWaitingJobFirstOnce: Move to the top goes first in the claim order, and the claim uses it up.
func TestTopPutsAWaitingJobFirstOnce(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	first := env.enqueue(t, 1, 1)
	env.enqueue(t, 2, 1)
	env.enqueue(t, 3, 1)

	if err := env.q.Top(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, first)
	if !got.Top {
		t.Fatal("top not recorded")
	}
	job := mustClaim(t, env, first)
	if job.Attempt != 1 {
		t.Fatalf("attempt = %d", job.Attempt)
	}
	if got, _ := env.q.Get(ctx, first); got.Top {
		t.Fatal("claim did not use up top")
	}
	// The rest keep the default order: newest first.
	if next, _ := env.q.Claim(ctx, "w", ClaimOptions{}); next == nil || next.ID != 3 {
		t.Fatalf("next claim = %v, want job 3", next)
	}
}

// TestTopAcceptsRetryWaitButNotRunning: a retry_wait job is shown as waiting, so it may be picked for the top; a
// running job cannot jump.
func TestTopAcceptsRetryWaitButNotRunning(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	waiting := env.enqueue(t, 1, 1)
	env.setState(t, waiting, RetryWait)
	if err := env.q.Top(ctx, waiting); err != nil {
		t.Fatalf("top on retry_wait: %v", err)
	}

	running := env.enqueue(t, 2, 1)
	if _, err := env.q.Claim(ctx, "w", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	err := env.q.Top(ctx, running)
	var changed *ErrChangedSince
	if !errors.As(err, &changed) {
		t.Fatalf("top on running = %v, want ErrChangedSince", err)
	}
}

// TestCancelKeepsTopAndRetryClearsIt: cancelling a job that was moved to the top keeps the flag, so a retry of it
// must clear it or the retried job would jump the queue without the person asking again.
func TestCancelKeepsTopAndRetryClearsIt(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	if err := env.q.Top(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.q.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := env.q.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.State != Waiting || got.Top {
		t.Fatalf("after retry: state %s top %v; want waiting with no top", got.State, got.Top)
	}
}

// TestFailureCodeIsKeptAndCleared: the failure's code is stored beside its sentence, and a retry clears it.
func TestFailureCodeIsKeptAndCleared(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{
		Verdict: VerdictFail, Reason: "the encoder stopped", Code: "encode.failed", FailedStep: "video",
		NextAction: "Press Retry to try the file again; if it fails again, the job log has the details.",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.FailureCode != "encode.failed" {
		t.Fatalf("failure code = %q, want encode.failed", got.FailureCode)
	}
	if err := env.q.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := env.q.Get(ctx, id); got.FailureCode != "" || got.NextAction != "" {
		t.Fatalf("after retry: code %q next %q; want both cleared", got.FailureCode, got.NextAction)
	}
}

// TestRetryRefusedForTheReplaceFailures: the three replace failures need a look at the file, not another run.
func TestRetryRefusedForTheReplaceFailures(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	for i, code := range []string{"replace.record_unsettled", "replace.put_back_failed", "replace.stranger_in_trash"} {
		id := env.enqueue(t, int64(i+1), 1)
		job := mustClaim(t, env, id)
		if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictFail, Reason: "a replace failed", Code: code}); err != nil {
			t.Fatal(err)
		}
		want, refused := RetryRefused(code)
		if !refused || want == "" {
			t.Fatalf("%s is not in the refusal table", code)
		}
		err := env.q.Retry(ctx, id)
		var notRetryable *ErrNotRetryable
		if !errors.As(err, &notRetryable) || notRetryable.Message != want {
			t.Fatalf("retry of %s = %v, want ErrNotRetryable %q", code, err, want)
		}
		if got := env.stateOf(t, id); got != Failed {
			t.Fatalf("refused retry moved %s to %s", code, got)
		}
	}
	// A job that failed before the code was recorded has none, and stays retryable (an old install).
	id := env.enqueue(t, 9, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictFail, Reason: "an old failure"}); err != nil {
		t.Fatal(err)
	}
	if err := env.q.Retry(ctx, id); err != nil {
		t.Fatalf("retry with no code: %v", err)
	}
}

// TestUsedUpTriesSaysSo: the third may-pass ends the job failed with the used-up sentence, not one that promises
// another automatic try.
func TestUsedUpTriesSaysSo(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	retry := Result{Verdict: VerdictRetry, Reason: "the file could not be checked", NextAction: "This file will be tried again in a few minutes."}
	for try := 1; try <= 3; try++ {
		job := mustClaim(t, env, id)
		if err := env.q.Finish(ctx, id, job.Attempt, retry); err != nil {
			t.Fatal(err)
		}
		if try < 3 {
			env.clk.advance(31 * 60 * 1e9)
		}
	}
	got, _ := env.q.Get(ctx, id)
	if got.State != Failed || got.NextAction != usedUpTries {
		t.Fatalf("after three may-passes: state %s next %q; want failed with %q", got.State, got.NextAction, usedUpTries)
	}
}

// TestRetryClearsTheOldOutputDetails (L-Q-3): the retried attempt starts with nothing from the last one on the row.
func TestRetryClearsTheOldOutputDetails(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{
		Verdict: VerdictFail, Reason: "a save failed", OutputPath: "/media/old-shrunk.mkv",
		SizeBefore: 1000, SizeAfter: 400,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.q.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.OutputPath != "" || got.SizeBefore != nil || got.SizeAfter != nil || got.Outcome != "" {
		t.Fatalf("after retry: output %q before %v after %v outcome %q; want all empty",
			got.OutputPath, got.SizeBefore, got.SizeAfter, got.Outcome)
	}
}
