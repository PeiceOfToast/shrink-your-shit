// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A kept job says newFileRefused: true on the list and on its detail, because its new file was refused and the
// original is still in place. Other jobs leave the key out: not false, not null.
func TestNewFileRefusedIsOnKeptJobsOnly(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)

	kept, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "kept.mkv"), SizeBefore: 1000})
	st.Claim("w")
	if err := st.Keep(kept, jobs.ReasonKeptOriginal, "the new file does not look like the original"); err != nil {
		t.Fatal(err)
	}
	failed, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "failed.mkv"), SizeBefore: 1000})
	st.Claim("w")
	if err := st.Fail(failed, "convert_failed", "Converting stopped with an error."); err != nil {
		t.Fatal(err)
	}
	doneWithOriginal(t, st, lib, "trashed.mkv", jobs.OriginalKept)

	// the list: the flag is true on the kept row, and absent (not false, not null) on the others
	byName := map[string]map[string]any{}
	for _, tab := range []string{"failed", "completed"} {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=" + tab + "&limit=200"})
		var list struct{ Items []map[string]any }
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("list %s: %v\n%s", tab, err, body)
		}
		for _, it := range list.Items {
			byName[it["name"].(string)] = it
		}
	}
	if len(byName) != 3 {
		t.Fatalf("lists have %d rows, want 3: %v", len(byName), byName)
	}
	if v, ok := byName["kept.mkv"]["newFileRefused"]; !ok || v != true {
		t.Errorf("list kept.mkv newFileRefused = %v (present %v), want true", v, ok)
	}
	for _, name := range []string{"failed.mkv", "trashed.mkv"} {
		if v, ok := byName[name]["newFileRefused"]; ok {
			t.Errorf("list %s has newFileRefused = %v, want the key absent", name, v)
		}
	}

	// the detail: the same rule on GET /api/v2/jobs/{id}
	detail := func(id int64) map[string]any {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		var d map[string]any
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatalf("detail %d: %v\n%s", id, err, body)
		}
		return d
	}
	if d := detail(kept); d["newFileRefused"] != true {
		t.Errorf("detail kept newFileRefused = %v, want true", d["newFileRefused"])
	}
	if d := detail(failed); d["newFileRefused"] != nil {
		t.Errorf("detail failed newFileRefused = %v, want the key absent", d["newFileRefused"])
	} else if _, ok := d["newFileRefused"]; ok {
		t.Errorf("detail failed has the key newFileRefused, want it absent")
	}
}
