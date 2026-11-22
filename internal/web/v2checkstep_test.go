// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// L1's check step is index -1 and sorts before the encode, so a failure summary that names the encode step (raw
// index 0) must be shown at its position among the steps: the second of two (review2 646 P3-1, mutation Md: the
// raw index survived every test).
func TestFailureViewCountsTheCheckStepInTheStepPosition(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/broken.mkv"})
	st.Claim("t")
	st.RecordRunStep(id, jobs.RunStep{Index: -1, Name: "preflight", Outcome: "ok"})
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "failed"})
	st.SetFailureSummary(id, jobs.FailureSummary{Step: 0, StepCount: 1, ExitCode: 1,
		KeyLines: []string{"Invalid data found when processing input"}})
	st.Fail(id, "convert_failed", "Converting stopped with an error.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d struct{ Failure map[string]any }
	json.Unmarshal([]byte(body), &d)
	if d.Failure == nil {
		t.Fatalf("no failure: %s", body)
	}
	if d.Failure["step"] != "encode" || d.Failure["stepIndex"] != 1.0 || d.Failure["stepCount"] != 2.0 {
		t.Errorf("failure step %v/%v of %v, want encode, 1 (its position after the check step), 2",
			d.Failure["step"], d.Failure["stepIndex"], d.Failure["stepCount"])
	}
}

// A job with no failure summary must not be read as failing at -1, which is the check step's index: its stderr
// lines would be attached to the check step as the failed step's output (review2 646 P3-1, mutation Me: the -1
// sentinel survived every test).
func TestANoSummaryJobAttachesNoOutputToItsCheckStep(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/good.mkv"})
	st.Claim("t")
	st.RecordRunStep(id, jobs.RunStep{Index: -1, Name: "preflight", Outcome: "ok"})
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok"})
	st.AppendLogLine(id, -1, "check", jobs.LevelInfo, "stderr", "a line a no-summary job must not call a failure")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct{ Steps []map[string]any }
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 2 || r.Steps[0]["name"] != "preflight" {
		t.Fatalf("steps: %s", body)
	}
	if out := r.Steps[0]["output"].([]any); len(out) != 0 {
		t.Errorf("the check step of a job with no failure summary got output %v", out)
	}
}

// The check's lines have their own section in the download log, between the source file and the encode (review2
// 646 P3-1, mutation Ml: dropping the section from the list survived every test).
func TestDownloadLogHasACheckSection(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	const check = "Checked the encoder for this file and the free space beside it."
	st.AppendLogLine(id, -1, "check", jobs.LevelInfo, "app", check)
	_, text, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log/download"})
	if !strings.Contains(text, "== Check before converting ==") || !strings.Contains(text, check) {
		t.Errorf("download has no check section:\n%s", text)
	}
	if strings.Index(text, "== Check before converting ==") > strings.Index(text, "== Other lines ==") &&
		strings.Contains(text, "== Other lines ==") {
		t.Errorf("the check lines fell to Other lines:\n%s", text)
	}
}
