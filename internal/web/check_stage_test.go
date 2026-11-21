// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// 12c: while the check after the encode runs, the job reads "Checking the result" with no ETA or speed. The
// worker keeps the encode's progress until the whole run ends, so those must come only from a running job.
func TestCheckingResultShowsNoEncodeETA(t *testing.T) {
	const id = int64(912001)
	worker.SetLiveForTest(id, worker.LiveJob{Worker: "w", Started: time.Now(),
		Progress: media.Progress{Percent: 100, ETASeconds: 2, Speed: 3}})
	defer worker.DropLiveForTest(id)

	p := v2Progress("validating", id, nil)
	if p["stage"] != "Checking the result" {
		t.Errorf("stage = %v, want Checking the result", p["stage"])
	}
	if p["etaS"] != nil || p["speed"] != nil {
		t.Errorf("checking shows the encode's eta %v and speed %v, want none", p["etaS"], p["speed"])
	}

	pct := 40.0
	r := v2Progress("running", id, &pct)
	if r["etaS"] != 2 {
		t.Errorf("converting etaS = %v, want 2 (the encode's own)", r["etaS"])
	}
}
