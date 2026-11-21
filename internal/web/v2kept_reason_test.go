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

// A kept job reads the same in the Jobs list as in its detail: its stored code and words. The original is kept,
// so design's failure sentence (which names the output folder for cannot_write) is not used for it.
func TestKeptJobReadsTheSameInTheListAndTheDetail(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	keep := func(name, code, words string) int64 {
		t.Helper()
		id, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 1000})
		if err != nil {
			t.Fatal(err)
		}
		st.Claim("w")
		if err := st.Keep(id, code, words); err != nil {
			t.Fatal(err)
		}
		return id
	}
	unwritable := keep("unwritable.mkv", jobs.ReasonCannotWrite,
		"the folder that keeps originals couldn't be made here, so the original was left as it is. Check that shrink-your-shit may write in this folder")
	older := keep("older.mkv", jobs.ReasonKeptOriginal, "the new file does not look like the original")

	reasonOf := func(m map[string]any) (string, string) {
		t.Helper()
		r, ok := m["reason"].(map[string]any)
		if !ok {
			t.Fatalf("no reason on %v", m)
		}
		return r["code"].(string), r["message"].(string)
	}
	list := func(query string) map[string]map[string]any {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?" + query + "&limit=200"})
		var v struct{ Items []map[string]any }
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("list %s: %v\n%s", query, err, body)
		}
		out := map[string]map[string]any{}
		for _, it := range v.Items {
			out[it["name"].(string)] = it
		}
		return out
	}
	detail := func(id int64) map[string]any {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		var d map[string]any
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatalf("detail %d: %v\n%s", id, err, body)
		}
		return d
	}

	rows := list("tab=failed")
	for name, id := range map[string]int64{"unwritable.mkv": unwritable, "older.mkv": older} {
		it, ok := rows[name]
		if !ok {
			t.Fatalf("the failed tab has no %s", name)
		}
		lc, lm := reasonOf(it)
		dc, dm := reasonOf(detail(id))
		if lc != dc || lm != dm {
			t.Errorf("%s: list says %q %q, detail says %q %q; want the same", name, lc, lm, dc, dm)
		}
	}
	if c, m := reasonOf(rows["unwritable.mkv"]); c != jobs.ReasonCannotWrite || strings.Contains(m, "output folder") || strings.Contains(m, "permission denied") {
		t.Errorf("cannot_write on the list: code %q message %q", c, m)
	}
	if c, _ := reasonOf(rows["older.mkv"]); c != jobs.ReasonKeptOriginal {
		t.Errorf("an older kept job lists as %q, want %q", c, jobs.ReasonKeptOriginal)
	}

	// the filter finds a kept job under the code the list shows; check_failed keeps working too
	for _, code := range []string{jobs.ReasonKeptOriginal, "check_failed"} {
		got := list("tab=failed&reason=" + code)
		if _, ok := got["older.mkv"]; !ok || len(got) != 1 {
			names := []string{}
			for n := range got {
				names = append(names, n)
			}
			t.Errorf("?reason=%s lists %v, want only older.mkv", code, names)
		}
	}
}
