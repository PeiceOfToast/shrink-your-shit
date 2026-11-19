// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// bytesNowOf is the overview's bytesNow for library l, from the same totals the overview reads.
func bytesNowOf(t *testing.T, s *Store, l Library) int64 {
	t.Helper()
	lt, err := s.LibraryTotalsOf(l)
	if err != nil {
		t.Fatal(err)
	}
	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	return BytesNow(lt, tot.Counts[l.ID])
}

// A converted file bigger than its original (kept by design) is in the library at its new size, so bytesNow
// counts that size: the recorded 300 plus the 100 it grew.
func TestBytesNowCountsAGrownFile(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, a, 400)
	if now := bytesNowOf(t, s, l); now != 400 {
		t.Errorf("bytesNow %d, want 400: the grown file is in the library at 400", now)
	}
}

// Once a scan finds that grown file gone, it no longer counts: bytesNow is 0, growth included.
func TestBytesNowDropsAGrownFileOnceGone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, a, 400)
	if err := s.SetOutputGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 0 {
		t.Errorf("bytesNow %d after the grown file is gone, want 0", now)
	}
}

// A grown file replaced by its conversion, its original kept in the trash: the library holds only the new
// file, at 400.
func TestBytesNowCountsAGrownReplacement(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, a, 400)
	ib, err := s.BeginSwap(Original{JobID: id, LibraryID: l.ID, Path: a, Trash: filepath.Join(l.Path, ".syp-trash", "a.mkv"), NewPath: a})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSwap(ib, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 400 {
		t.Errorf("bytesNow %d, want 400: the library holds only the grown file", now)
	}
}

// A folder output outside the library, the original kept where it was: the library holds the original at 300,
// the new file is not in it, so bytesNow stays 300 (#403).
func TestBytesNowIgnoresGrowthOfAFolderOutputOutsideTheLibrary(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(t.TempDir(), "a.mkv"), 400)
	if now := bytesNowOf(t, s, l); now != 300 {
		t.Errorf("bytesNow %d, want 300: the 400-byte output is not in the library", now)
	}
}
