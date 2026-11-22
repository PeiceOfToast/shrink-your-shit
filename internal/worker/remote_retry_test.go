// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A remote worker's converter stopped by the system (out of memory, killed) is tried again, as a local
// one is: the server's copy of the failure is the report's words, so the killed code it carries is what
// says the system stopped it, and the retry's words are the local path's.
func TestRemoteConverterKilledIsTriedAgain(t *testing.T) {
	s := newScenario(t)
	w := s.retrying()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	job, err := s.store.Claim("remote-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.store.LeaseOf(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	rj := NewRemoteJob(lease)
	go func() {
		<-rj.Planned()
		rj.Deliver(RemoteResult{Stderr: "Killed", Err: errors.New("the other computer couldn't convert it"), Code: jobs.ReasonKilled})
	}()
	w.RunRemote(context.Background(), job, rj)
	s.wantRetry(1, 5*time.Minute, "Converting stopped with an error")
}
