// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"testing"
)

// The Workers page reads the job a worker is on and what it finished today (page 10).

func claimAs(t *testing.T, env *testEnv, workerID string, id int64) Job {
	t.Helper()
	job, err := env.q.Claim(context.Background(), workerID, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != id {
		t.Fatalf("claim as %s gave %v, want job %d", workerID, job, id)
	}
	return *job
}

func TestRunningForIsTheWorkersNewestRunningJob(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	if j, err := env.q.RunningFor(ctx, "local"); err != nil || j != nil {
		t.Fatalf("RunningFor with nothing running = %v, %v; want nil", j, err)
	}
	id := env.enqueue(t, 1, 1)
	job := claimAs(t, env, "local", id)
	j, err := env.q.RunningFor(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if j == nil || j.ID != id {
		t.Fatalf("RunningFor = %+v, want job %d", j, id)
	}
	if j2, err := env.q.RunningFor(ctx, "another"); err != nil || j2 != nil {
		t.Errorf("RunningFor for another worker = %+v, %v; want nil", j2, err)
	}
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictDone, Outcome: "smaller"}); err != nil {
		t.Fatal(err)
	}
	if j, err := env.q.RunningFor(ctx, "local"); err != nil || j != nil {
		t.Errorf("RunningFor after the finish = %+v, %v; want nil", j, err)
	}
}

func TestDoneSinceCountsTheWorkersFinishedJobs(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	job := claimAs(t, env, "local", id)
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictDone, Outcome: "smaller"}); err != nil {
		t.Fatal(err)
	}
	since := fromMS(env.clk.ms - 1000)
	if n, err := env.q.DoneSince(ctx, "local", since); err != nil || n != 1 {
		t.Errorf("DoneSince(local, an hour ago) = %d, %v; want 1", n, err)
	}
	if n, err := env.q.DoneSince(ctx, "another", since); err != nil || n != 0 {
		t.Errorf("DoneSince(another) = %d, %v; want 0", n, err)
	}
	if n, err := env.q.DoneSince(ctx, "local", fromMS(env.clk.ms+1000)); err != nil || n != 0 {
		t.Errorf("DoneSince(local, in an hour) = %d, %v; want 0", n, err)
	}
}
