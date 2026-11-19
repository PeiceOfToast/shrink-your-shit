// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file converted twice (its source changed) is one file: Libraries' completed count is the Dashboard's
// library done count, not one per job.
func TestLibraryCompletedCountsDistinctFilesLikeTheDashboard(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	// files go under l.Path, the stored (resolved) folder: macOS /var is /private/var, Windows RUNNER~1 is the long name
	a := filepath.Join(l.Path, "tr2.mp4")
	b := filepath.Join(l.Path, "other.mkv")
	finish := func(path string) {
		t.Helper()
		id, err := st.Enqueue(jobs.Job{LibraryID: l.ID, Path: path, SizeBefore: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := st.Complete(id, path+".out.mkv", 400); err != nil {
			t.Fatal(err)
		}
	}
	finish(a)
	// the overview caches its library totals; a later completion must show on both pages
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"}); code != 200 {
		t.Fatalf("overview: %d %s", code, body)
	}
	finish(b)
	finish(a) // the same file again

	libs := jsonAt(t, srv, "/api/v2/libraries", "items")
	overview := jsonAt(t, srv, "/api/v2/overview", "libraries")
	var libCompleted, libSkipped, dashDone int
	for _, it := range libs {
		m := it.(map[string]any)
		c := m["counts"].(map[string]any)
		libCompleted, libSkipped = int(c["completed"].(float64)), int(c["skipped"].(float64))
	}
	for _, it := range overview {
		m := it.(map[string]any)
		dashDone = int(m["counts"].(map[string]any)["done"].(float64))
	}
	if libCompleted != 2 || dashDone != 2 || libCompleted != dashDone {
		t.Errorf("completed: Libraries %d, Dashboard done %d; want 2 and 2 (two distinct files)", libCompleted, dashDone)
	}
	if libSkipped != 0 {
		t.Errorf("skipped: Libraries %d, want 0", libSkipped)
	}
}

// jsonAt GETs path and returns the array at key of its JSON object.
func jsonAt(t *testing.T, srv *httptest.Server, path, key string) []any {
	t.Helper()
	code, body, _ := do(t, nil, srv, call{method: "GET", path: path})
	var out map[string]any
	if code != 200 || json.Unmarshal([]byte(body), &out) != nil {
		t.Fatalf("GET %s: %d %s", path, code, body)
	}
	items, _ := out[key].([]any)
	return items
}
