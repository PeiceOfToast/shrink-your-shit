// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
)

// completeCheck is the store, with a look at the job's record at the moment it is marked done.
type completeCheck struct {
	*jobs.Store
	logs *runlog.Logs
	seen func(steps []jobs.Step, log string)
}

func (c completeCheck) Complete(id int64, out string, size int64) error {
	steps, _ := c.Store.Steps(id)
	log, _, _ := c.logs.Read(id, 0)
	c.seen(steps, log)
	return c.Store.Complete(id, out, size)
}

// Review 7484a34: when a job's state says done, its record is already complete (the run log's "done"
// line and the last steps), so a stream that ends on done never misses them.
func TestDoneComesAfterTheLogAndSteps(t *testing.T) {
	s := newScenario(t)
	logs := &runlog.Logs{Dir: filepath.Join(t.TempDir(), "logs")}
	called := false
	w := s.worker(Replace)
	w.Logs = logs
	w.Queue = completeCheck{Store: s.store, logs: logs, seen: func(steps []jobs.Step, log string) {
		called = true
		names := []string{}
		for _, st := range steps {
			names = append(names, st.Step)
		}
		if !strings.Contains(strings.Join(names, ","), "verified") || !strings.Contains(strings.Join(names, ","), "replaced") {
			t.Errorf("steps when done: %v", names)
		}
		if !strings.Contains(log, " done, ") {
			t.Errorf("run log when done: %q", log)
		}
	}}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Complete was never called")
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("%s %s", j.Status, j.Error)
	}
}
