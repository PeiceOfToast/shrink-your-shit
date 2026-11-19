// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The headline and the library's done count are files, not conversions: a file converted twice (two job
// rows, both done) is one file done.
func TestDoneCountsFilesNotConversions(t *testing.T) {
	s := open(t)
	// files go under l.Path, the stored (resolved) folder: macOS /var is /private/var, Windows RUNNER~1 is the long name
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	corrupt := filepath.Join(l.Path, "corrupt.mp4")
	other := filepath.Join(l.Path, "other.mkv")
	for _, p := range []string{corrupt, corrupt, other} {
		id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: p, SizeBefore: 1000})
		s.Claim("w")
		if err := s.Complete(id, p, 400); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := s.DoneCount(); err != nil || n != 2 {
		t.Errorf("DoneCount %d (%v), want 2 files", n, err)
	}
	tot, err := s.LibraryTotalsOf(l)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Done != 2 {
		t.Errorf("library Done %d, want 2 files", tot.Done)
	}
	if _, done := sumDays(dayCounts(t, s)); done != 2 {
		t.Errorf("daily completed %d, want 2 (it counts files by latest end)", done)
	}
}
