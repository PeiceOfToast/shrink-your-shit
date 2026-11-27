// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// TestClaimReturnsTheJobWhenThePickedEventFails: the picked line goes out after the claim commits. If the bus
// refuses it, the job is already running, so a claim error would drop a job nobody holds until the next start's
// Recover. The claim succeeds and the missing line is logged instead.
func TestClaimReturnsTheJobWhenThePickedEventFails(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	if err := env.bus.Close(ctx); err != nil {
		t.Fatal(err)
	}

	job, err := env.q.Claim(ctx, "w1", ClaimOptions{})
	if err != nil {
		t.Fatalf("claim with a closed bus: %v", err)
	}
	if job == nil || job.ID != id || job.Attempt != 1 {
		t.Fatalf("claim = %+v, want job %d attempt 1", job, id)
	}
	got, err := env.q.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != Running || got.WorkerID != "w1" {
		t.Fatalf("job after the failed event = %s by %q, want running by w1", got.State, got.WorkerID)
	}
}

// TestOneOpenJobPerFileIsRejectedByTheIndex pins the schema itself: enqueue's checks are in the same statement,
// but the partial unique index is the guarantee, so a raw second open row must be refused.
func TestOneOpenJobPerFileIsRejectedByTheIndex(t *testing.T) {
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	first := env.enqueue(t, 1, 1)

	err := env.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO jobs (file_id, library_id, workflow_id, path, state, queued_at, queued_by)
			VALUES (1, 1, 1, '/media/1.mkv', 'waiting', ?, 'test')`, ms(env.clk.now()))
		return err
	})
	if err == nil {
		t.Fatalf("a second open row for file 1 was inserted beside job %d", first)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("error = %v, want the unique index to refuse it", err)
	}

	// The index is partial: a finished row does not block a new open one.
	if _, err := env.q.Cancel(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := env.enqueue(t, 1, 1)
	if second == first {
		t.Fatalf("enqueue reused the cancelled job %d", first)
	}
}

// TestHistoryEventsCarryTheirKindAndJob covers every History kind a verb emits, not only requeued: the page reads
// these by kind, so a wrong kind or a missing job id would go unnoticed.
func TestHistoryEventsCarryTheirKindAndJob(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)

	queued, stopQueued := env.bus.Subscribe(events.Filter{Kind: events.KindQueued})
	defer stopQueued()
	id := env.enqueue(t, 1, 1)
	expectEvent(t, queued, events.KindQueued, id)

	picked, stopPicked := env.bus.Subscribe(events.Filter{Kind: events.KindPicked})
	defer stopPicked()
	job := mustClaim(t, env, id)
	expectEvent(t, picked, events.KindPicked, id)

	retried, stopRetried := env.bus.Subscribe(events.Filter{Kind: events.KindRetry})
	defer stopRetried()
	if err := env.q.Finish(ctx, id, job.Attempt, Result{Verdict: VerdictRetry, Reason: "ffmpeg failed"}); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, retried, events.KindRetry, id)

	cancelled, stopCancelled := env.bus.Subscribe(events.Filter{Kind: events.KindCancelled})
	defer stopCancelled()
	if _, err := env.q.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, cancelled, events.KindCancelled, id)
}

// TestCancelClearsTheWorkerOnTheHeadRow: a cancelled job is not anybody's any more, so the History page must not
// show a worker on it (Requeue clears the column the same way).
func TestCancelClearsTheWorkerOnTheHeadRow(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	env.addLibrary(t, 1, false)
	id := env.enqueue(t, 1, 1)
	mustClaim(t, env, id)

	res, err := env.q.Cancel(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WasRunning || res.WorkerID != "w" {
		t.Fatalf("cancel result = %+v, want the running worker", res)
	}
	var worker sql.NullString
	if err := env.db.Read().QueryRow(`SELECT worker_id FROM jobs WHERE id = ?`, id).Scan(&worker); err != nil {
		t.Fatal(err)
	}
	if worker.Valid {
		t.Fatalf("worker_id = %q on a cancelled row, want NULL", worker.String)
	}
}

// expectEvent reads the next event on ch and checks it is the kind and job asked for.
func expectEvent(t *testing.T, ch <-chan events.Event, kind events.Kind, id int64) {
	t.Helper()
	select {
	case e := <-ch:
		if e.Kind != kind || e.JobID != id || e.Source != "jobs" {
			t.Fatalf("event = %+v, want %s for job %d from jobs", e, kind, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event for job %d", kind, id)
	}
}
