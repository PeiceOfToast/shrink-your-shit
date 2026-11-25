// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library deleted after its jobs failed: its failures leave the summary's Needs attention list and its 24 h
// count, and stay in the jobs log.
func TestNeedsAttentionLeavesOutADeletedLibrary(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	gone, err := st.AddLibrary("Gone", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := st.AddLibrary("Kept", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	failIn := func(lib jobs.Library, name string) {
		id, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 10})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := st.Fail(id, jobs.ReasonConvertFailed, "Converting did not finish."); err != nil {
			t.Fatal(err)
		}
	}
	failIn(gone, "gone-film.mkv")
	failIn(kept, "kept-film.mkv")
	if err := st.DeleteLibrary(gone.ID); err != nil {
		t.Fatal(err)
	}

	_, sb, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
	if !strings.Contains(sb, "kept-film.mkv") || strings.Contains(sb, "gone-film.mkv") {
		t.Errorf("needs attention should list only the kept library's failure: %s", sb)
	}
	if !strings.Contains(sb, `"failed24h":1`) {
		t.Errorf("the failed count in 24 h should be 1: %s", sb)
	}

	_, jb, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	if !strings.Contains(jb, "gone-film.mkv") || !strings.Contains(jb, "kept-film.mkv") {
		t.Errorf("the jobs log should still show both failures: %s", jb)
	}
}
