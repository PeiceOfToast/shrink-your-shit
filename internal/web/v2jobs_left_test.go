// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Done tab's left filter is server-side, like kept: the page and the total come from the filtered rows, and
// it is asked for on the completed tab only, and not together with kept.
func TestV2JobsLeftFilterOnTheCompletedTab(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	for _, name := range []string{"leftA.mkv", "leftB.mkv"} {
		id, _ := st.Enqueue(jobs.Job{LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 1000})
		st.Claim("w")
		if err := st.Complete(id, filepath.Join(elsewhere, name+".hevc.mkv"), 400); err != nil {
			t.Fatal(err)
		}
	}
	doneWithOriginal(t, st, lib, "replaced.mkv", "")

	get := func(query string) (int, int) {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?" + query})
		if code != 200 {
			return code, 0
		}
		return code, decode[v2List](t, body).Total
	}

	if code, total := get("tab=completed&left=1"); code != 200 || total != 2 {
		t.Errorf("left=1: %d total %d, want 2 left in place", code, total)
	}
	if code, total := get("tab=completed&left=0"); code != 200 || total != 3 {
		t.Errorf("left=0: %d total %d, want every done file (3)", code, total)
	}
	for _, tab := range []string{"waiting", "failed", "skipped", "running"} {
		if code, _ := get("tab=" + tab + "&left=1"); code != 400 {
			t.Errorf("left=1 on the %s tab: %d, want 400", tab, code)
		}
	}
	if code, _ := get("tab=completed&left=1&kept=1"); code != 400 {
		t.Errorf("left=1 with kept=1: %d, want 400", code)
	}
	if code, _ := get("tab=completed&left=x"); code != 400 {
		t.Errorf("left=x: %d, want 400", code)
	}
}
