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

// ui-shop 07:07Z: jobId is null only for a scan's own skip. A job the person stopped, waiting or
// running, is listed under Skipped (cancelled) with its jobId, and its detail keeps its steps.
func TestStoppedJobKeepsItsJob(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	waiting, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.AddStep(waiting, "found", true, "Found.")
	if _, err := st.RequestStop(waiting); err != nil {
		t.Fatal(err)
	}
	running, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "b.mkv")})
	st.Claim("w")
	st.AddStep(running, "found", true, "Found.")
	st.RequestStop(running)
	if err := st.UserStopped(running); err != nil { // its worker ends it
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=skipped"})
	var l struct {
		Items []struct {
			Name, State string
			JobID       *string
		}
	}
	json.Unmarshal([]byte(body), &l)
	want := map[string]int64{"a.mkv": waiting, "b.mkv": running}
	if len(l.Items) != 2 {
		t.Fatalf("%s", body)
	}
	for _, it := range l.Items {
		if it.State != "cancelled" || it.JobID == nil || *it.JobID != strconv.FormatInt(want[it.Name], 10) {
			t.Errorf("%+v in %s", it, body)
		}
	}
	for _, id := range []int64{waiting, running} {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		var d struct {
			Removed bool
			State   string
			Steps   []struct{ Step string }
		}
		json.Unmarshal([]byte(body), &d)
		if !d.Removed || d.State != "cancelled" || len(d.Steps) != 1 {
			t.Errorf("job %d: %s", id, body)
		}
	}
}

// ui-shop 10:54Z: a removed job's detail names its file (not "Job 8"), however it went: with its
// library, stopped by the person, or skipped by its worker.
func TestRemovedJobKeepsItsFile(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	other, _ := st.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	withLib, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	stopped, _ := st.Enqueue(jobs.Job{Library: other.Path, LibraryID: other.ID, Path: filepath.Join(other.Path, "b.mkv")})
	st.RequestStop(stopped)
	skipped, _ := st.Enqueue(jobs.Job{Library: other.Path, LibraryID: other.ID, Path: filepath.Join(other.Path, "c.mkv")})
	st.Claim("w")
	st.Skip(skipped, "x", rules.SkipLeftByWorkflow)
	if code, b, _ := do(t, nil, srv, call{method: "DELETE", path: "/api/v2/libraries/" + strconv.FormatInt(lib.ID, 10), ctype: "application/json"}); code >= 300 {
		t.Fatalf("delete %d %s", code, b)
	}
	for id, path := range map[int64]string{withLib: filepath.Join(lib.Path, "a.mkv"), stopped: filepath.Join(other.Path, "b.mkv"), skipped: filepath.Join(other.Path, "c.mkv")} {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		var d struct {
			Removed bool
			File    *struct{ Path, Name, Folder string }
		}
		json.Unmarshal([]byte(body), &d)
		if !d.Removed || d.File == nil || d.File.Path != path || d.File.Name != filepath.Base(path) || d.File.Folder != filepath.Dir(path) {
			t.Errorf("job %d: %s", id, body)
		}
	}
}
