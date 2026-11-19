// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library's skipped count is the one Jobs > Skipped lists for it: a scan's skip (a file record, no job)
// and a worker's skip (a file record written by the job) both count, once each.
func TestLibrarySkippedIsWhatTheSkippedListShows(t *testing.T) {
	s := open(t)
	root := t.TempDir() // paths are built with filepath, so the library range matches on every OS
	l, _ := s.AddLibrary("Movies", root, rules.SliderTemplate(3), true)
	if err := s.PutFile(File{Path: filepath.Join(root, "broken.mkv"), Decision: "unreadable", Reason: rules.SkipUnreadable}); err != nil {
		t.Fatal(err)
	}
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: filepath.Join(root, "hevc.mkv"), SizeBefore: 1000})
	s.Claim("w")
	if err := s.Skip(id, "too small to bother", "too_small"); err != nil {
		t.Fatal(err)
	}
	totals, err := s.LibraryTotalsOf(l)
	if err != nil {
		t.Fatal(err)
	}
	_, _, cards, err := s.FileList(FileFilter{Show: CardSkipped, LibraryID: l.ID})
	if err != nil {
		t.Fatal(err)
	}
	if totals.Skipped != 2 || cards[CardSkipped] != 2 {
		t.Errorf("library skipped: overview %d, Jobs list %d; want 2 and 2", totals.Skipped, cards[CardSkipped])
	}
}
