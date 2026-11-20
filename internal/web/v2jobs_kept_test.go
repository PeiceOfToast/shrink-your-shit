// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// doneWithOriginal adds a done job for name in lib. state is the state of its original: jobs.OriginalKept
// (the swap finished, the original is in the trash), restored or purged (after a kept swap), or "" for a job
// that replaced its file with nothing kept.
func doneWithOriginal(t *testing.T, st *jobs.Store, lib jobs.Library, name, state string) {
	t.Helper()
	p := filepath.Join(lib.Path, name)
	id, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	st.Claim("w")
	if err := st.Complete(id, p, 400); err != nil {
		t.Fatal(err)
	}
	if state == "" {
		return
	}
	oid, err := st.BeginSwap(jobs.Original{JobID: id, LibraryID: lib.ID, Path: p, Trash: filepath.Join(lib.Path, ".syp-trash", name),
		Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400, NewPath: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(oid, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state != jobs.OriginalKept {
		if err := st.SetSwapState(oid, state); err != nil {
			t.Fatal(err)
		}
	}
}

// The Done tab's kept filter is server-side: the page and the total both come from the filtered rows.
func TestV2JobsKeptFilterOnTheCompletedTab(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	other, _ := st.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), true)
	doneWithOriginal(t, st, lib, "keptA.mkv", jobs.OriginalKept)
	doneWithOriginal(t, st, lib, "keptB.mkv", jobs.OriginalKept)
	doneWithOriginal(t, st, lib, "restored.mkv", jobs.OriginalRestored)
	doneWithOriginal(t, st, lib, "purged.mkv", jobs.OriginalPurged)
	doneWithOriginal(t, st, lib, "replaced.mkv", "")
	doneWithOriginal(t, st, other, "keptE.mkv", jobs.OriginalKept)

	// get returns the status, the total and the names of the rows on the page.
	get := func(query string) (int, int, []string) {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?" + query})
		if code != 200 {
			return code, 0, nil
		}
		v := decode[v2List](t, body)
		var names []string
		for _, it := range v.Items {
			names = append(names, it["name"].(string))
		}
		sort.Strings(names)
		return code, v.Total, names
	}
	same := func(got []string, want ...string) bool {
		sort.Strings(want)
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	libID := lib.ID

	if code, total, names := get("tab=completed&kept=1"); code != 200 || total != 3 || !same(names, "keptA.mkv", "keptB.mkv", "keptE.mkv") {
		t.Errorf("kept=1: %d total %d names %v, want 3 kept files in both libraries", code, total, names)
	}
	if _, total, names := get("tab=completed&kept=1&libraryId=" + itoa(libID)); total != 2 || !same(names, "keptA.mkv", "keptB.mkv") {
		t.Errorf("kept=1 with libraryId: total %d names %v, want 2 in the first library", total, names)
	}
	if _, total, names := get("tab=completed&kept=1&q=keptA"); total != 1 || !same(names, "keptA.mkv") {
		t.Errorf("kept=1 with q: total %d names %v, want keptA.mkv", total, names)
	}
	if _, total, names := get("tab=completed&kept=1&q=keptA&libraryId=" + itoa(other.ID)); total != 0 || !same(names) {
		t.Errorf("kept=1 with q and the other library: total %d names %v, want none", total, names)
	}
	// paging: the page holds the filtered rows, and the total is the filtered count
	if _, total, names := get("tab=completed&kept=1&limit=2&offset=2"); total != 3 || len(names) != 1 {
		t.Errorf("kept=1 page 2 of 2: total %d, %d names %v, want total 3 and one row", total, len(names), names)
	}
	if _, total, _ := get("tab=completed&kept=0"); total != 5 {
		t.Errorf("kept=0: total %d, want every done file but the restored one (5)", total)
	}
	if _, total, _ := get("tab=completed"); total != 5 {
		t.Errorf("no kept param: total %d, want 5 (restored.mkv is skipped, not done)", total)
	}
	for _, tab := range []string{"waiting", "failed", "skipped", "running"} {
		if code, _, _ := get("tab=" + tab + "&kept=1"); code != 400 {
			t.Errorf("kept=1 on the %s tab: %d, want 400", tab, code)
		}
	}
	if code, _, _ := get("tab=completed&kept=x"); code != 400 {
		t.Errorf("kept=x: %d, want 400", code)
	}
}
