// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Jobs page keeps its own words, folders included: only notifications are cut to file names (internal/notify).
func TestJobsStillNamesTheOutputFolder(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: filepath.Join(t.TempDir(), "a.mkv")})
	st.Claim("w")
	if err := st.Fail(id, jobs.ReasonCannotWrite, "The file was not written."); err != nil {
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 {
		t.Fatalf("failed tab: %s", body)
	}
	jobsSays, _ := r.Items[0]["reason"].(map[string]any)["message"].(string)
	if want := "Couldn't write to the output folder: permission denied."; jobsSays != want {
		t.Errorf("Jobs says %q, want its own words %q", jobsSays, want)
	}
	if got := FailureSentence(st, id); got != jobsSays {
		t.Errorf("FailureSentence %q, Jobs %q: the notification source must stay the Jobs sentence", got, jobsSays)
	}
}
