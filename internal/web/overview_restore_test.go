// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A restore writes no event, yet the restored file stops counting as converted: the Dashboard and Libraries
// show it on the next fetch, not after the overview's caches expire (QA #431).
func TestRestoreShowsAtOnceOnTheOverviewAndLibraries(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	doneWithOriginal(t, st, lib, "stays.mkv", jobs.OriginalKept)
	p := filepath.Join(lib.Path, "back.mkv")
	id, err := st.Enqueue(jobs.Job{LibraryID: lib.ID, Path: p, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	st.Claim("w")
	if err := st.Complete(id, p, 400); err != nil {
		t.Fatal(err)
	}
	oid, err := st.BeginSwap(jobs.Original{JobID: id, LibraryID: lib.ID, Path: p, Trash: filepath.Join(lib.Path, ".syp-trash", "back.mkv"),
		Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400, NewPath: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(oid, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// counts returns the headline's converted files, the Dashboard's library done, Libraries' completed and
	// the daily bars' completed
	counts := func() [4]int {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
		var o struct {
			Headline  struct{ FilesConverted int }
			Libraries []struct{ Counts struct{ Done int } }
			Daily     []struct{ Completed int }
		}
		if code != 200 || json.Unmarshal([]byte(body), &o) != nil || len(o.Libraries) != 1 {
			t.Fatalf("overview: %d %s", code, body)
		}
		var c [4]int
		c[0], c[1] = o.Headline.FilesConverted, o.Libraries[0].Counts.Done
		for _, d := range o.Daily {
			c[3] += d.Completed
		}
		libs := jsonAt(t, srv, "/api/v2/libraries", "items")
		c[2] = int(libs[0].(map[string]any)["counts"].(map[string]any)["completed"].(float64))
		return c
	}
	if got := counts(); got != [4]int{2, 2, 2, 2} {
		t.Fatalf("before the restore: headline, dashboard library, Libraries, daily = %v, want all 2", got)
	}
	if err := st.SetSwapState(oid, jobs.OriginalRestored); err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != [4]int{1, 1, 1, 1} {
		t.Errorf("right after the restore: headline, dashboard library, Libraries, daily = %v, want all 1", got)
	}
}
