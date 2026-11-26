// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// putSettling records path as the scanner does for a file still changing.
func putSettling(t *testing.T, st *jobs.Store, path string) {
	t.Helper()
	if err := st.PutFile(jobs.File{Path: path, Size: 7, ModTime: 1, Decision: "skip: " + rules.SkipWords[rules.SkipStillChanging], Reason: rules.SkipStillChanging}); err != nil {
		t.Fatal(err)
	}
}

// Manager 11-26 (a): a file still changing is waiting to settle, not skipped. The Jobs screen lists it first on
// the waiting tab as a scan row (state queued, subState settling; ui-shop 11-26) and counts it there, never under Skipped.
func TestSettlingFileIsWaitingNotSkipped(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	other, _ := st.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	putSettling(t, st, filepath.Join(lib.Path, "copying.mkv"))
	putSettling(t, st, filepath.Join(other.Path, "elsewhere.mkv"))
	st.PutFile(jobs.File{Path: filepath.Join(lib.Path, "b.mkv"), Decision: "skip: " + rules.SkipWords[rules.SkipModernFormat], Reason: rules.SkipModernFormat})

	type item struct {
		ID, Name, Origin, State, LibraryID, LogState string
		SubState                                     *string
		JobID, Position, Priority, Pin, RetryAt      any
		Attempts                                     int
		Bytes                                        int64
		Reason                                       *struct{ Code, Message string }
		Detail                                       *string
		Status                                       *string
		Tags                                         *[]string
	}
	var l struct {
		Items  []item
		Total  int
		Counts map[string]int
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&libraryId=" + itoa(lib.ID)})
	json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 2 || l.Total != 2 || l.Counts["waiting"] != 2 || l.Counts["skipped"] != 1 {
		t.Fatalf("waiting tab for one library: %s", body)
	}
	s := l.Items[0]
	if !strings.HasPrefix(s.ID, "settle-") || s.Name != "copying.mkv" || s.Origin != "scan" || s.State != "queued" || s.SubState == nil || *s.SubState != "settling" ||
		s.JobID != nil || s.Position != nil || s.Priority != nil || s.Pin != nil || s.RetryAt != nil || s.Attempts != 0 || s.Bytes != 7 ||
		s.LibraryID != itoa(lib.ID) || s.Status != nil || s.Tags == nil || s.LogState != "none" ||
		s.Reason == nil || s.Reason.Code != rules.SkipStillChanging || s.Reason.Message != settlingWords || s.Detail == nil || *s.Detail != settlingWords {
		t.Errorf("settling row: %+v\n%s", s, body)
	}
	if l.Items[1].Name != "a.mkv" || l.Items[1].Origin != "job" {
		t.Errorf("the job comes after the settling file: %s", body)
	}
	if !strings.Contains(body, `"jobId":null`) || !strings.Contains(body, `"position":null`) {
		t.Errorf("jobId and position are null, not left out: %s", body)
	}

	// a name filter applies to it as to the jobs
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&q=a.mkv"})
	l.Items = nil
	json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 1 || l.Items[0].Name != "a.mkv" {
		t.Errorf("q=a.mkv: %s", body)
	}

	// Skipped leaves it out, in its list and its count
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=skipped"})
	l.Items = nil
	json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 1 || l.Items[0].Name != "b.mkv" || l.Counts["skipped"] != 1 || l.Counts["waiting"] != 3 {
		t.Errorf("skipped tab: %s", body)
	}
	// the v1 Skipped route too
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"})
	if strings.Contains(body, "copying.mkv") || !strings.Contains(body, "b.mkv") {
		t.Errorf("/api/activity/skipped: %s", body)
	}
	// it has no job to open
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + s.ID}); code != 404 {
		t.Errorf("GET %s: %d, want 404", s.ID, code)
	}
}

// Pages run across the settling files and then the jobs, with nothing listed twice or missed.
func TestSettlingPagesIntoTheJobs(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	for _, n := range []string{"s1.mkv", "s2.mkv", "s3.mkv"} {
		putSettling(t, st, filepath.Join(lib.Path, n))
	}
	for _, n := range []string{"j1.mkv", "j2.mkv"} {
		st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, n)})
	}
	var names []string
	for _, off := range []string{"0", "2", "4"} {
		var l struct {
			Items []struct{ Name string }
			Total int
		}
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&limit=2&offset=" + off})
		json.Unmarshal([]byte(body), &l)
		if l.Total != 5 {
			t.Fatalf("offset %s: total %d, want 5: %s", off, l.Total, body)
		}
		for _, it := range l.Items {
			names = append(names, it.Name)
		}
	}
	if got := strings.Join(names, ","); got != "s1.mkv,s2.mkv,s3.mkv,j1.mkv,j2.mkv" {
		t.Errorf("pages: %s", got)
	}
}
