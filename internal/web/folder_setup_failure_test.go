// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A job that fails while setting up its output folder never ran ffmpeg. It is stored as convert_failed, and Jobs must
// show the worker's own sentence, not "ffmpeg stopped with an error."
func TestFolderSetupFailureShowsTheWorkersSentence(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("w")
	words := "The folder for new files can't be written to, so nothing was converted. Your original was not touched. Check the flow's folder, then press Retry."
	if err := st.Fail(id, jobs.ReasonConvertFailed, words); err != nil {
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 {
		t.Fatalf("failed tab: %s", body)
	}
	if got := r.Items[0]["reason"].(map[string]any)["message"]; got != words {
		t.Errorf("Jobs says %q, want the worker's sentence %q", got, words)
	}
}
