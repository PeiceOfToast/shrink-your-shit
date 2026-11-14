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
	if j.Status != Queued || j.Attempts != 1 || j.RetryAt == "" || j.Worker != "" {
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
