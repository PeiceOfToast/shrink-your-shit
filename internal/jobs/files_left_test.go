// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// convertTo runs one conversion of path (size before) that writes out and leaves the original where it was.
func convertTo(t *testing.T, s *Store, l Library, path, out string, before, after int64) int64 {
	t.Helper()
	id, err := s.Enqueue(Job{LibraryID: l.ID, Path: path, SizeBefore: before})
	if err != nil {
		t.Fatal(err)
	}
	s.Claim("w")
	if err := s.Complete(id, out, after); err != nil {
		t.Fatal(err)
	}
	return id
}

// The Done tab's left filter lists exactly the rows the could-free number sums: the original left in place
// (not kept, not gone, not changed), each file once by its newest conversion.
func TestFileListLeftIsTheCouldFreeSet(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")

	// left in another folder: its original is still there
	convertTo(t, s, l, filepath.Join(l.Path, "left.mkv"), filepath.Join(elsewhere, "left.hevc.mkv"), 1000, 400)

	// kept in the kept store: its original is in the trash, not left in place
	kept := convertTo(t, s, l, filepath.Join(l.Path, "kept.mkv"), filepath.Join(elsewhere, "kept.hevc.mkv"), 1000, 400)
	oid, err := s.BeginSwap(Original{JobID: kept, LibraryID: l.ID, Path: filepath.Join(l.Path, "kept.mkv"),
		Trash: filepath.Join(l.Path, ".syp-trash", "kept.mkv"), Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400,
		NewPath: filepath.Join(elsewhere, "kept.hevc.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSwap(oid, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// original gone: a scan found it removed, so it is freed, not could-free
	gone := convertTo(t, s, l, filepath.Join(l.Path, "gone.mkv"), filepath.Join(elsewhere, "gone.hevc.mkv"), 1000, 400)
	if err := s.SetOriginalGone(gone); err != nil {
		t.Fatal(err)
	}

	// converted twice, both times left in place: listed once, by the newer conversion
	convertTo(t, s, l, filepath.Join(l.Path, "twice.mkv"), filepath.Join(elsewhere, "twice-1.hevc.mkv"), 1000, 400)
	convertTo(t, s, l, filepath.Join(l.Path, "twice.mkv"), filepath.Join(elsewhere, "twice-2.hevc.mkv"), 2000, 500)

	rows, total, _, err := s.FileList(FileFilter{Show: CardProcessed, Left: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var sum int64
	for _, r := range rows {
		names = append(names, filepath.Base(r.Path))
		sum += r.SizeBefore - r.SizeAfter
	}
	sort.Strings(names)
	if want := []string{"left.mkv", "twice.mkv"}; len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("left rows %v, want %v", names, want)
	}
	if total != 2 {
		t.Errorf("total %d, want 2 (the filtered count)", total)
	}

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	if tot.Summary.CouldFreeBytes != sum {
		t.Errorf("could-free %d, but the left rows sum to %d", tot.Summary.CouldFreeBytes, sum)
	}
	if sum != 600+1500 {
		t.Errorf("sum %d, want 2100 (left 600 + twice 1500)", sum)
	}
}
