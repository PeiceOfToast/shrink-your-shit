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
	// files go under l.Path, the stored (resolved) folder: macOS /var is /private/var, Windows RUNNER~1 is the long name
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err := s.PutFile(File{Path: filepath.Join(l.Path, "broken.mkv"), Decision: "unreadable", Reason: rules.SkipUnreadable}); err != nil {
		t.Fatal(err)
	}
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: filepath.Join(l.Path, "hevc.mkv"), SizeBefore: 1000})
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

// A file still changing waits to settle (manager 11-26): the all-library skipped counts leave it out, as the
// Skipped list does (review 743: /api/activity/summary said 2 while the list had 1).
func TestSettlingFileIsNotCountedSkipped(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.PutFile(File{Path: filepath.Join(l.Path, "broken.mkv"), Decision: "unreadable", Reason: rules.SkipUnreadable})
	s.PutFile(File{Path: filepath.Join(l.Path, "copying.mkv"), Decision: "skip: " + rules.SkipWords[rules.SkipStillChanging], Reason: rules.SkipStillChanging})
	a, err := s.ActivitySummary()
	if err != nil {
		t.Fatal(err)
	}
	totals, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	_, _, cards, err := s.FileList(FileFilter{Show: CardSkipped})
	if err != nil {
		t.Fatal(err)
	}
	if a.Skipped != 1 || totals.Summary.Skipped != 1 || cards[CardSkipped] != 1 {
		t.Errorf("skipped: activity %d, overview %d, Jobs list %d; want 1 each", a.Skipped, totals.Summary.Skipped, cards[CardSkipped])
	}
}
