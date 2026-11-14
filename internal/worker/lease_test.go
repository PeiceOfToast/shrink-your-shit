// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// V7a: a worker whose job was taken back (its lease ran out while it was stuck) never puts the new file
// in place; the original is untouched and the job goes back in the queue. A lost worker isn't the job's fault,
// so it may be taken again at once: no retry wait (see jobs.RequeueExpiredLeases).
func TestLostLeaseNeverCommits(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	swapHook = func(step string) error {
		if step == "before swap" { // stuck here long enough for the lease to run out, and the sweeper ran
			old := jobs.LeaseFor
			jobs.LeaseFor = -time.Second
			lease, _ := s.store.LeaseOf(s.id)
			s.store.RenewLease(s.id, lease)
			jobs.LeaseFor = old
			s.store.RequeueExpiredLeases(time.Minute)
		}
		return nil
	}
	defer func() { swapHook = nil }()
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Queued || j.RetryAt != "" {
		t.Errorf("the job should be back in the queue with no retry wait: %s retryAt=%q %q", j.Status, j.RetryAt, j.Error)
	}
	s.assertOriginalSafe()
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Errorf("the original changed: %q", b)
	}
}
