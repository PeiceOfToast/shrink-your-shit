// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A try that converted and then failed is followed, in the store, by its failure step (the worker writes it
// before RetryLater). Its next try, once running, reads "running", not "validating" from the old converted step.
func TestRetriedJobReadsRunningNotValidating(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	defer st.Close()
	id, _ := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/a.mkv"})
	st.Claim("w")
	st.AddStep(id, "converted", true, "Converted.")
	st.AddFailureStep(id, "", "Try 1 of 3 didn't work: the check could not be done. Trying again in 5 minutes.")
	st.RetryLater(id, time.Now().Add(-time.Minute), "check_failed", "the check could not be done")
	if _, err := st.Claim("w"); err != nil {
		t.Fatalf("the retry is not taken: %v", err)
	}
	steps, _ := st.Steps(id)
	pct := 0.0
	if got := v2State(string(jobs.Running), steps, &pct); got != "running" {
		t.Errorf("state %q with steps %+v, want running", got, steps)
	}
}
