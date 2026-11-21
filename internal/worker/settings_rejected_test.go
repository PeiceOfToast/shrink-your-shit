// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Slice 15: the remote worker's result decides the job's stored reason. Its report carried settings_rejected:
// the job is that code. An older worker's report carried none: the job keeps today's convert_failed.
func TestRemoteFailureCodeBecomesTheJobsReason(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
		ok   bool
	}{
		{&remoteFailure{code: jobs.ReasonSettingsRejected, err: errors.New("x")}, jobs.ReasonSettingsRejected, true},
		{&remoteFailure{err: errors.New("x")}, "", true},
		{errors.New("x"), "", false},
	} {
		got, ok := remoteFailureCode(c.err)
		if got != c.code || ok != c.ok {
			t.Errorf("%v: %q %v, want %q %v", c.err, got, ok, c.code, c.ok)
		}
	}
	if got := reasonCode(failedWithCode(jobs.ReasonSettingsRejected, "x", errors.New("y"))); got != jobs.ReasonSettingsRejected {
		t.Errorf("a coded failure: %s, want %s", got, jobs.ReasonSettingsRejected)
	}
	if got := reasonCode(failed("x", errors.New("y"))); got != jobs.ReasonConvertFailed {
		t.Errorf("an uncoded failure: %s, want %s", got, jobs.ReasonConvertFailed)
	}
}
