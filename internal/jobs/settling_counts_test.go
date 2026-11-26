// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file still changing is waiting to settle (Jobs > Waiting lists it), so the library and overview waiting
// counts include it, and the buckets add up: a file must not sit in no bucket while it settles.
func TestSettlingFilesCountAsWaiting(t *testing.T) {
	s := open(t)
	lib, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	other, _ := s.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), true)
	s.Enqueue(Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	settling := func(path string) {
		t.Helper()
		if err := s.PutFile(File{Path: path, Size: 7, ModTime: 1, Decision: "skip: " + rules.SkipWords[rules.SkipStillChanging], Reason: rules.SkipStillChanging}); err != nil {
			t.Fatal(err)
		}
	}
	settling(filepath.Join(lib.Path, "copying.mkv"))
	settling(filepath.Join(other.Path, "elsewhere.mkv"))

	c, err := s.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if got := c[lib.ID].Waiting; got != 2 {
		t.Errorf("library waiting = %d, want 2 (its queued job and its settling file)", got)
	}
	if got := c[other.ID].Waiting; got != 1 {
		t.Errorf("other library waiting = %d, want 1", got)
	}

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	if got := tot.Counts[lib.ID].Waiting; got != 2 {
		t.Errorf("overview library waiting = %d, want 2", got)
	}
	if got := tot.Counts[other.ID].Waiting; got != 1 {
		t.Errorf("overview other library waiting = %d, want 1", got)
	}
	if got := tot.Summary.WaitingFiles; got != 3 {
		t.Errorf("overview waiting files = %d, want 3", got)
	}

	a, err := s.ActivitySummary()
	if err != nil {
		t.Fatal(err)
	}
	if a.WaitingFiles != 3 {
		t.Errorf("activity waiting files = %d, want 3", a.WaitingFiles)
	}
}
