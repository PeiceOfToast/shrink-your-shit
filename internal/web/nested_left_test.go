// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library inside another gives its files up to the inner one (BytesBefore). A beside output is the inner
// library's: its bytes belong to the inner library's bytesNow only, and the outer keeps its own 1000.
func TestNestedLibraryLeftOutputsStayWithTheirOwnLibrary(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false) // a new store starts stopped: the test's worker has to be started to claim a job
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	outer, err := st.AddLibrary("Outer", root, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil { // the inner folder must exist, or it keeps the unresolved path
		t.Fatal(err)
	}
	inner, err := st.AddLibrary("Inner", sub, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	st.PutFile(jobs.File{Path: filepath.Join(outer.Path, "own.mkv"), Size: 1000})
	a := filepath.Join(inner.Path, "a.mkv")
	st.PutFile(jobs.File{Path: a, Size: 300})
	id, err := st.Enqueue(jobs.Job{LibraryID: inner.ID, Path: a, SizeBefore: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, filepath.Join(inner.Path, "a.hevc.mkv"), 400); err != nil {
		t.Fatal(err)
	}

	libs, err := st.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	var c libraryTotals
	totals, err := c.get(st, libs)
	if err != nil {
		t.Fatal(err)
	}
	tot, err := st.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	if now := jobs.BytesNow(totals[outer.ID], tot.Counts[outer.ID]); now != 1000 {
		t.Errorf("outer bytesNow %d, want 1000: its own file; the inner library's original and output are the inner's", now)
	}
	if now := jobs.BytesNow(totals[inner.ID], tot.Counts[inner.ID]); now != 700 {
		t.Errorf("inner bytesNow %d, want 700: the original (300) and the beside output (400)", now)
	}
}
