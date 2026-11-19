// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A notification's failure sentence is the one the Jobs list shows for the same job. A folder-setup failure is stored
// as convert_failed (worker.go), which Jobs words as its ffmpeg sentence, not the folder words; the notification says
// the same as Jobs, whatever Jobs says.
func TestFailureSentenceIsWhatJobsShows(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("w")
	words := "The folder for new files can't be written to, so nothing was converted. Your original was not touched. Check the flow's folder, then press Retry."
	if err := st.Fail(id, "convert_failed", words); err != nil {
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 {
		t.Fatalf("failed tab: %s", body)
	}
	jobsSays := r.Items[0]["reason"].(map[string]any)["message"]
	if got := FailureSentence(st, id); got == "" || got != jobsSays {
		t.Errorf("notification %q, Jobs %q: want the same sentence", got, jobsSays)
	}
	if got := FailureSentence(st, id+99); got != "" {
		t.Errorf("a job that is not there has sentence %q", got)
	}
}
