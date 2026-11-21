// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
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

// 12c: the Overview row shows the encode's ETA and speed while the job converts, and none once the check has
// started (the converted step is written). The Files card follows the same rule.
func TestOverviewRowShowsNoEncodeETADuringCheck(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	id, err := st.Enqueue(jobs.Job{Path: "/m/a.mkv", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("builtin"); err != nil {
		t.Fatal(err)
	}
	worker.SetLiveForTest(id, worker.LiveJob{Worker: "builtin", Started: time.Now(),
		Progress: media.Progress{Percent: 100, ETASeconds: 2, Speed: 3, FPS: 40}})
	defer worker.DropLiveForTest(id)

	row := func() map[string]any {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
		var out struct{ Active []map[string]any }
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		for _, a := range out.Active {
			if a["jobId"] == strconv.FormatInt(id, 10) {
				return a
			}
		}
		t.Fatalf("job %d not in overview active list: %s", id, body)
		return nil
	}

	if r := row(); r["etaS"] != float64(2) || r["speed"] != float64(3) {
		t.Fatalf("converting row etaS %v speed %v, want the encode's 2 and 3", r["etaS"], r["speed"])
	}
	if err := st.AddStep(id, "converted", true, "Converted."); err != nil {
		t.Fatal(err)
	}
	r := row()
	if r["stage"] != "Checking the result" {
		t.Errorf("stage = %v, want Checking the result", r["stage"])
	}
	for _, k := range []string{"etaS", "speed", "fps"} {
		if r[k] != nil {
			t.Errorf("checking row %s = %v, want none", k, r[k])
		}
	}
}
