// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V7a: a running job is held by its lease; renewed it stays; run out, the job goes back to the queue
// with why (worker_lost, a timeline step), and the old lease holds nothing any more.
func TestLeases(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	lease, _ := s.LeaseOf(id)
	if lease == "" {
		t.Fatal("no lease")
	}
	if ok, _ := s.RenewLease(id, lease); !ok {
		t.Error("renew")
	}
	if ok, _ := s.HoldsLease(id, lease); !ok {
		t.Error("holds")
	}
	if n, _ := s.RequeueExpiredLeases(time.Minute); n != 0 {
		t.Errorf("a live lease was taken back (%d)", n)
	}
	// it runs out
	old := LeaseFor
	LeaseFor = -time.Second
	s.RenewLease(id, lease)
	LeaseFor = old
	if n, err := s.RequeueExpiredLeases(time.Minute); n != 1 || err != nil {
		t.Fatalf("requeued %d %v", n, err)
	}
	j, _ := s.Get(id)
	if j.Status != Queued || j.Attempts != 0 || j.RetryAt != "" || j.Worker != "" {
		t.Errorf("back in the queue: %+v", j)
	}
	ev, _ := s.JobEvents(id)
	if last := ev[len(ev)-1]; last.Reason != ReasonWorkerLost || last.Message == "" {
		t.Errorf("why %+v", last)
	}
	steps, _ := s.Steps(id)
	if last := steps[len(steps)-1]; last.Step != "lease" || last.OK {
		t.Errorf("timeline %+v", last)
	}
	if ok, _ := s.HoldsLease(id, lease); ok {
		t.Error("the old lease still holds")
	}
	if ok, _ := s.RenewLease(id, lease); ok {
		t.Error("the old lease renewed")
	}
}

// Once the server has accepted a worker's done report it finishes the job with no worker renewing the lease.
// The lease's own time runs out during that finish; the expired-lease sweep must not requeue the job, and the
// finish must end it Done. A report from a worker that no longer holds the lease is refused.
func TestAFinishingJobIsNotRequeued(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv"})
	if _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	lease, _ := s.LeaseOf(id)
	if ok, err := s.HoldThroughFinish(id, "not-the-lease"); ok || err != nil {
		t.Fatalf("a stranger's lease held the finish: %v %v", ok, err)
	}
	now = now.Add(LeaseFor + time.Minute) // the worker's lease has run out by now
	if ok, err := s.HoldThroughFinish(id, lease); !ok || err != nil {
		t.Fatalf("the holder's report was refused: %v %v", ok, err)
	}
	if n, err := s.RequeueExpiredLeases(0); n != 0 || err != nil {
		t.Fatalf("the sweep took a job the server is finishing: %d %v", n, err)
	}
	if err := s.Complete(id, "/m/a.mkv", 1); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Done {
		t.Errorf("finished job is %s, want done", j.Status)
	}
}

// A worker that vanished is not the job's fault: the job goes back in the queue at once, and the try is not counted.
func TestLostWorkerJobIsRetakeableAtOnce(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv"})
	if _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(LeaseFor + time.Minute)
	if n, err := s.RequeueExpiredLeases(5 * time.Minute); n != 1 || err != nil {
		t.Fatalf("requeued %d %v", n, err)
	}
	j, _ := s.Get(id)
	if j.Status != Queued || j.Attempts != 0 || j.RetryAt != "" {
		t.Fatalf("lost worker's job: status %s, attempts %d, retry_at %q; want queued, 0, none", j.Status, j.Attempts, j.RetryAt)
	}
	if got, err := s.Claim("w2"); err != nil || got.ID != id {
		t.Errorf("another worker could not take it at once: %v %v", got.ID, err)
	}
}

// A job that keeps losing its worker gets the normal wait on the MaxLostInARow-th loss, and that loss is counted.
// A real failure in between starts the count again.
func TestLostWorkerGuardBacksOff(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	wait := 5 * time.Minute
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv"})
	lose := func() Job {
		t.Helper()
		if _, err := s.Claim("w1"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(LeaseFor + time.Minute)
		if n, err := s.RequeueExpiredLeases(wait); n != 1 || err != nil {
			t.Fatalf("requeued %d %v", n, err)
		}
		j, _ := s.Get(id)
		return j
	}
	for i := 1; i < MaxLostInARow; i++ {
		if j := lose(); j.Attempts != 0 || j.RetryAt != "" {
			t.Fatalf("loss %d: attempts %d, retry_at %q; want 0 and none", i, j.Attempts, j.RetryAt)
		}
	}
	j := lose()
	if j.Attempts != 1 || j.RetryAt == "" {
		t.Fatalf("loss %d: attempts %d, retry_at %q; want 1 and a wait", MaxLostInARow, j.Attempts, j.RetryAt)
	}
	if at, _ := time.Parse(time.RFC3339, j.RetryAt); !at.Equal(now.Add(wait).Truncate(time.Second)) {
		t.Errorf("retry_at %s, want %s", at, now.Add(wait))
	}
	// the wait is over, and a real failure, then a loss: the count starts again, so that loss is retakeable at once
	now = now.Add(wait)
	if _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryLater(id, now.Add(wait), "disk_full", "The disk is full."); err != nil {
		t.Fatal(err)
	}
	now = now.Add(wait)
	if j := lose(); j.Attempts != 2 || j.RetryAt != "" {
		t.Errorf("a loss after a real failure: attempts %d, retry_at %q; want 2 (the counted loss and the failure) and none", j.Attempts, j.RetryAt)
	}
}

// A real failure still waits the full time it asked for, and is counted.
func TestRealFailureStillBacksOff(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv"})
	if _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	at := now.Add(5 * time.Minute)
	if err := s.RetryLater(id, at, "disk_full", "The disk is full."); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Get(id)
	if j.Attempts != 1 || j.RetryAt != at.UTC().Format(time.RFC3339) {
		t.Errorf("real failure: attempts %d, retry_at %q; want 1 and %s", j.Attempts, j.RetryAt, at.UTC().Format(time.RFC3339))
	}
}
