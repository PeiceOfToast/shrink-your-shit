// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// #692: a job refused before the encode (no ffmpeg ran) names the preflight step in the API, not a step that
// does not exist. The worker records the preflight at index -1; the card reads "preflight", step 1 of 1.
func TestRefusedBeforeTheEncodeNamesThePreflightStep(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	f1, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/refused.mkv"})
	st.Claim("t")
	const preflight = -1 // the worker's stepCheck: the check before the encode
	st.RecordRunStep(f1, jobs.RunStep{Index: preflight, Name: "preflight", Outcome: "failed",
		Result: "Graphics card only: no graphics encoder for this file."})
	st.SetFailureSummary(f1, jobs.FailureSummary{Step: preflight, StepCount: 1, ExitCode: -1, FfmpegRan: false,
		KeyLines: []string{"Graphics card only: no graphics encoder for this file."}})
	st.Fail(f1, jobs.ReasonHardwareChoice, "Graphics card only: no graphics encoder for this file.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 {
		t.Fatalf("failed tab: %s", body)
	}
	f, _ := r.Items[0]["failure"].(map[string]any)
	if f == nil {
		t.Fatalf("no failure on the refused job: %s", body)
	}
	if f["step"] != "preflight" || f["stepIndex"] != 0.0 || f["stepCount"] != 1.0 || f["ffmpegRan"] != false {
		t.Errorf("failure %v, want step preflight, stepIndex 0, stepCount 1, ffmpegRan false", f)
	}
	if h, _ := f["hint"].(string); h == "" {
		t.Errorf("no next-step hint on the refused job: %v", f)
	}
}
