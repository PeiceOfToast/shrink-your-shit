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

// The log's steps carry "found": the lines the step's check wrote, in order, for the drawer's "What the check
// found". A kept job has no failure output, so its check lines must still reach the drawer.
func TestJobLogStepsCarryWhatTheCheckFound(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	st.AppendLogLine(id, 0, "app", "started")
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok"})
	st.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "check", Outcome: "failed", Result: "the new file does not look like the original"})
	st.AppendLogLine(id, 1, "check", "Check: playback test, 10 s sample from 0:00:00")
	st.AppendLogLine(id, 1, "check", "Where: playback reached frame 42")
	st.Keep(id, jobs.ReasonKeptOriginal, "the new file does not look like the original")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		Steps []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("log %s", body)
	}
	found := func(i int) []any { return r.Steps[i]["found"].([]any) }
	if f := found(1); len(f) != 2 || f[0] != "Check: playback test, 10 s sample from 0:00:00" || f[1] != "Where: playback reached frame 42" {
		t.Errorf("check step found %v", f)
	}
	if f := found(0); len(f) != 0 {
		t.Errorf("encode step with no check lines found %v", f)
	}
	if out := r.Steps[1]["output"].([]any); len(out) != 0 {
		t.Errorf("a kept job has no failure output, got %v", out)
	}
	if !strings.Contains(body, `"found":[]`) {
		t.Errorf("a step with no check lines must say found: [], not null: %s", body)
	}
}

// A failed job keeps its output exactly as before; the new "found" does not change it.
func TestFailedJobKeepsItsOutputAndFoundIsEmpty(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "failed", Command: "ffmpeg -i in.mkv out.mkv"})
	st.AppendLogLine(id, 0, "stderr", "Invalid data found when processing input")
	st.SetFailureSummary(id, jobs.FailureSummary{Step: 0, StepCount: 1, ExitCode: 1, KeyLines: []string{"Invalid data found when processing input"}})
	st.Fail(id, "convert_failed", "Converting stopped with an error.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		Steps []struct {
			Output []string
			Found  []string
		}
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 1 || len(r.Steps[0].Output) != 1 || r.Steps[0].Output[0] != "Invalid data found when processing input" {
		t.Errorf("failed step output %s", body)
	}
	if r.Steps[0].Found == nil || len(r.Steps[0].Found) != 0 {
		t.Errorf("found %v, want an empty list", r.Steps[0].Found)
	}
}

// An imported log's one step has no check of its own: found is an empty list, not null.
func TestImportedStepFoundIsEmpty(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	st.AppendLogLine(id, 0, "app", "line 1")
	st.Complete(id, "/m/a.hevc.mkv", 100)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	if !strings.Contains(body, `"name":"imported"`) || !strings.Contains(body, `"found":[]`) {
		t.Errorf("imported step must carry found: [] : %s", body)
	}
}
