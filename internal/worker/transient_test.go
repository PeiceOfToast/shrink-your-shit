// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// A transient build error sends the job to retry without running flow; a permanent one still fails.
func TestBuildErrorsRetryOrFailByTransience(t *testing.T) {
	src := &fakeSource{}
	src.push(Work{Job: jobs.Job{ID: 1, Attempt: 1}, Err: errors.New("database is locked"), Transient: true})
	src.push(Work{Job: jobs.Job{ID: 2, Attempt: 1}, Err: errors.New("the file is no longer in its library")})
	run := func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result {
		t.Error("flow must not run for a job whose run could not be built")
		return flow.Result{}
	}
	w := New(Config{Source: src, Run: run, DataDir: t.TempDir(), Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()
	waitFor(t, "both build errors settled", func() bool {
		f, _, _, _ := src.snapshot()
		return len(f) == 2
	})
	f, _, _, _ := src.snapshot()
	if r := f[0]; r.Verdict != jobs.VerdictRetry {
		t.Errorf("transient build error: verdict %q, want retry", r.Verdict)
	}
	if r := f[1]; r.Verdict != jobs.VerdictFail {
		t.Errorf("permanent build error: verdict %q, want fail", r.Verdict)
	}
}
