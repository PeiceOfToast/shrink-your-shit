// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V6 review P3: a job its workflow left alone after its tries is listed under Skipped with its jobId,
// and its detail keeps its steps and tries; a scan's skip has no job.
func TestSkippedByWorkerKeepsItsJob(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	st.AddFailureStep(id, "n1", "Convert video didn't work: x")
	if err := st.Skip(id, "Convert video didn't work after 3 tries, so the file was left as it is.", rules.SkipLeftByWorkflow); err != nil {
		t.Fatal(err)
	}
	st.PutFile(jobs.File{Path: filepath.Join(lib.Path, "b.mkv"), Decision: "skip: " + rules.SkipWords[rules.SkipModernFormat], Reason: rules.SkipModernFormat})
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=skipped"})
	var l struct {
		Items []struct {
			Name   string
			JobID  *string
			Reason struct{ Code, Message string }
		}
	}
	json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 2 || l.Items[0].Name != "a.mkv" || l.Items[0].JobID == nil || *l.Items[0].JobID != strconv.FormatInt(id, 10) || l.Items[1].JobID != nil ||
		l.Items[0].Reason.Message != "Convert video didn't work after 3 tries, so the file was left as it is." {
		t.Fatalf("%s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d struct {
		Removed  bool
		Attempts int
		Steps    []struct{ Step, Words string }
		Reason   struct{ Message string }
	}
	json.Unmarshal([]byte(body), &d)
	if !d.Removed || d.Attempts != 1 || len(d.Steps) != 1 || !strings.Contains(d.Steps[0].Words, "didn't work") ||
		d.Reason.Message != "Convert video didn't work after 3 tries, so the file was left as it is." {
		t.Errorf("%s", body)
	}
}
