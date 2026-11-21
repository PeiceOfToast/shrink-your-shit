// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A job its workflow stopped ("Stop as failed") stores workflow_stopped, and the job detail serves that code,
// not ffmpeg_error (ui-shop QA of #612): no conversion went wrong, the workflow ended the path. The sentence
// stays the workflow's own words, and the timeline's last entry says the same.
func TestJobDetailServesWorkflowStoppedWithTheWorkflowsWords(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: "/m/remux.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	words := "Not a remux: nothing to gain. The original is untouched."
	if err := st.Fail(id, jobs.ReasonWorkflowStopped, words); err != nil {
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d struct {
		Reason struct{ Code, Message string } `json:"reason"`
		Events []struct {
			Reason struct{ Code, Message string } `json:"reason"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if d.Reason.Code != jobs.ReasonWorkflowStopped || d.Reason.Message != words {
		t.Errorf("the detail says code %q %q, want %q with the workflow's words %q", d.Reason.Code, d.Reason.Message, jobs.ReasonWorkflowStopped, words)
	}
	if n := len(d.Events); n == 0 || d.Events[n-1].Reason.Code != jobs.ReasonWorkflowStopped {
		t.Errorf("the timeline's last entry: %+v, want it to say %q too", d.Events, jobs.ReasonWorkflowStopped)
	}
}

// dev-review2 P3-2 of #615: the failures filter can name the new code, so ?reason=workflow_stopped lists the
// jobs it is on. Before the filter knew it, the request was refused as an unknown reason.
func TestJobsFilterListsWorkflowStopped(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	stopped, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/stopped.mkv"})
	st.Claim("t")
	st.Fail(stopped, jobs.ReasonWorkflowStopped, "Not a remux: nothing to gain. The original is untouched.")
	other, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/other.mkv"})
	st.Claim("t")
	st.Fail(other, jobs.ReasonConvertFailed, "Converting did not finish.")
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&reason=workflow_stopped&limit=200"})
	if code != 200 {
		t.Fatalf("?reason=workflow_stopped: %d %s", code, body)
	}
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 || r.Items[0]["name"] != "stopped.mkv" {
		t.Errorf("workflow_stopped: %s, want only stopped.mkv", body)
	}
}
