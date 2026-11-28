// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"errors"
	"testing"
)

// finishedJob makes a job done (or skipped) through the real claim and finish.
func finishedJob(t *testing.T, env *testEnv, file int64, v Verdict) int64 {
	t.Helper()
	id := env.enqueue(t, file, 1)
	job := mustClaim(t, env, id)
	if err := env.q.Finish(context.Background(), id, job.Attempt, Result{Verdict: v, Outcome: "smaller", SkipReason: "already_target", Reason: "Already HEVC", OutputPath: "/media/out.mkv", SizeBefore: 10, SizeAfter: 5}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRunAgainDoneAndSkippedRunAgain: a done or a skipped job goes back to waiting on its own row.
func TestRunAgainDoneAndSkippedRunAgain(t *testing.T) {
	ctx := context.Background()
	for _, v := range []Verdict{VerdictDone, VerdictSkip} {
		env := newEnv(t)
		env.addLibrary(t, 1, false)
		id := finishedJob(t, env, 1, v)
		if err := env.q.RunAgain(ctx, id); err != nil {
			t.Fatalf("%s: run again: %v", v, err)
		}
		if got := env.stateOf(t, id); got != Waiting {
			t.Fatalf("%s: state = %s, want waiting", v, got)
		}
		again := mustClaim(t, env, id)
		if again.ID != id || again.Attempt != 2 {
			t.Fatalf("%s: the claim is job %d attempt %d; want the same row on attempt 2", v, again.ID, again.Attempt)
		}
	}
}

// TestRunAgainRefusesOtherStates: anything that is not done or skipped is refused with its state.
func TestRunAgainRefusesOtherStates(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)

	waiting := env.enqueue(t, 1, 1)
	failed := env.enqueue(t, 2, 1)
	job := mustClaim(t, env, failed)
	if err := env.q.Finish(ctx, failed, job.Attempt, Result{Verdict: VerdictFail, Reason: "bad"}); err != nil {
		t.Fatal(err)
	}
	cancelled := env.enqueue(t, 3, 1)
	if _, err := env.q.Cancel(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	running := env.enqueue(t, 4, 1)
	mustClaim(t, env, running)

	for _, c := range []struct {
		name string
		id   int64
		want State
	}{{"waiting", waiting, Waiting}, {"failed", failed, Failed}, {"cancelled", cancelled, Cancelled}, {"running", running, Running}} {
		err := env.q.RunAgain(ctx, c.id)
		var nr *ErrNotRunnable
		if !errors.As(err, &nr) || nr.State != c.want {
			t.Errorf("%s: run again = %v, want ErrNotRunnable in %s", c.name, err, c.want)
		}
		if got := env.stateOf(t, c.id); got != c.want {
			t.Errorf("%s: state changed to %s", c.name, got)
		}
	}
}

// TestRunAgainClearsTheOldOutputAndUsesThePublishedRevision: the old output details go, and the run uses the workflow
// as published now, as a manual Retry does.
func TestRunAgainClearsTheOldOutputAndUsesThePublishedRevision(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := finishedJob(t, env, 1, VerdictDone)
	if err := env.q.RunAgain(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, _ := env.q.Get(ctx, id)
	if got.OutputPath != "" || got.SizeAfter != nil || got.SizeBefore != nil || got.Outcome != "" {
		t.Errorf("old output details kept: output %q outcome %q", got.OutputPath, got.Outcome)
	}
	if got.WorkflowRev != 0 {
		t.Errorf("revision %d kept; want the published one", got.WorkflowRev)
	}
}

// TestRunAgainStaysOneRowForTheFile: a scan that finds the file while it runs again does not queue it a second time:
// the file's one open job is this row (Enqueue answers with it).
func TestRunAgainStaysOneRowForTheFile(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := finishedJob(t, env, 1, VerdictDone)
	if err := env.q.RunAgain(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, open, err := env.q.Enqueue(ctx, 1, 1, 1, 1, "/media/1.mkv", "scan")
	if err != nil {
		t.Fatal(err)
	}
	if !open || got != id {
		t.Fatalf("scan enqueue = job %d open %v, want the same job %d already open", got, open, id)
	}
}

// TestRunAgainRefusedWhileTheFileIsBusy: another open job for the file blocks it, as Retry does.
func TestRunAgainRefusedWhileTheFileIsBusy(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := finishedJob(t, env, 1, VerdictDone)
	other := env.enqueue(t, 1, 1)
	err := env.q.RunAgain(ctx, id)
	var busy *ErrFileBusy
	if !errors.As(err, &busy) || busy.JobID != other {
		t.Fatalf("run again = %v, want ErrFileBusy for %d", err, other)
	}
}
