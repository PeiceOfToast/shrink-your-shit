// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// wrapperExec runs the encode as a real program (a wrapper that leaves a child holding its output, as
// QA's stall wrapper did) and everything else through the scenario's fake.
type wrapperExec struct {
	*media.FakeExec
	started chan struct{}
}

func (x wrapperExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for _, a := range args {
		if a == "libx265" {
			close(x.started)
			return media.OSExec{}.Stream(ctx, onLine, "sh", "-c", "sleep 30 & sleep 30")
		}
	}
	return x.FakeExec.Stream(ctx, onLine, name, args...)
}

// QA finding B: the stall watchdog and the person's stop land together on an encode whose wrapper left
// a child holding its output. The job still ends at once (one of the two stops wins), the worker is
// free again, and the original is untouched.
func TestStallAndUserStopTogetherEndTheJob(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.StallAfter = time.Hour // the test fires the watchdog's stop itself, with the person's
	x := wrapperExec{FakeExec: s.exec, started: make(chan struct{})}
	w.Exec = x
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	select {
	case <-x.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the encode never started")
	}
	time.Sleep(100 * time.Millisecond) // the wrapper and its child are running
	var both sync.WaitGroup
	both.Add(2)
	go func() { defer both.Done(); w.stop(s.id, stopStall) }()
	go func() { defer both.Done(); s.store.RequestStop(s.id); w.StopFile(s.id) }()
	both.Wait()
	select {
	case <-done:
	case <-time.After(media.StopWaitDelay + 3*time.Second):
		t.Fatal("the job is still running after both stops")
	}
	if w.busy() {
		t.Error("the worker is still busy")
	}
	if j, err := s.store.Get(s.id); err == nil && j.Status == jobs.Running {
		t.Errorf("still running: %+v", j)
	}
	s.assertOriginalSafe()
}
