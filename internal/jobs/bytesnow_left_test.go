// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A beside output inside the library is a file the library holds next to its original: the original (300,
// recorded) plus the new file (400). Its files row is never refreshed, so bytesNow adds the new file.
func TestBytesNowCountsABesideOutputInTheLibrary(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(l.Path, "a.hevc.mkv"), 400)
	if now := bytesNowOf(t, s, l); now != 700 {
		t.Errorf("bytesNow %d, want 700: the original (300) and the beside output (400) are both in the library", now)
	}
}

// A folder output outside the library is not in its bytes: bytesNow stays the original's 300.
func TestBytesNowIgnoresAFolderOutputOutsideTheLibrary(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(t.TempDir(), "a.mkv"), 400)
	if now := bytesNowOf(t, s, l); now != 300 {
		t.Errorf("bytesNow %d, want 300: an output outside the library is not in its bytes", now)
	}
}

// The beside output deleted by the person: it drops out again, the original (300) is what is left.
func TestBytesNowDropsABesideOutputOnceDeleted(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(l.Path, "a.hevc.mkv"), 400)
	if err := s.SetOutputGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 300 {
		t.Errorf("bytesNow %d after the beside output is gone, want 300", now)
	}
}

// The original of a beside conversion deleted, the smaller output kept: the library holds only the output
// (100). The saving freed (200) comes off the original's recorded 300, and nothing is added back.
func TestBytesNowCountsOnlyTheBesideOutputOnceTheOriginalIsGone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(l.Path, "a.hevc.mkv"), 100)
	if err := s.SetOriginalGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 100 {
		t.Errorf("bytesNow %d after the original is gone, want 100: only the beside output is left", now)
	}
}

// The same, with a beside output bigger than its original: it is the library's only file, at its new size.
func TestBytesNowCountsAGrownBesideOutputOnceTheOriginalIsGone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(l.Path, "a.hevc.mkv"), 400)
	if err := s.SetOriginalGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 400 {
		t.Errorf("bytesNow %d after the original is gone, want 400: only the beside output is left", now)
	}
}

// Both gone: nothing of the conversion is in the library.
func TestBytesNowIsZeroWhenTheBesideOriginalAndOutputAreBothGone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(l.Path, "a.hevc.mkv"), 100)
	if err := s.SetOriginalGone(id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutputGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 0 {
		t.Errorf("bytesNow %d with the original and the output both gone, want 0", now)
	}
}

// A folder output outside the library, its original then gone: the library holds nothing of the file.
func TestBytesNowIsZeroForAFolderOutputOutsideOnceTheOriginalIsGone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(id, filepath.Join(t.TempDir(), "a.mkv"), 100)
	if err := s.SetOriginalGone(id); err != nil {
		t.Fatal(err)
	}
	if now := bytesNowOf(t, s, l); now != 0 {
		t.Errorf("bytesNow %d, want 0: the original is gone and its output is outside the library", now)
	}
}

// A file converted twice beside itself (a republish): only its newest conversion's output is in the library.
func TestBytesNowCountsOnlyTheNewestBesideConversionOfAFile(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	a := filepath.Join(l.Path, "a.mkv")
	s.PutFile(File{Path: a, Size: 300})
	first, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(first, filepath.Join(l.Path, "a.first.mkv"), 100)
	second, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 300})
	s.Claim("w")
	s.Complete(second, filepath.Join(l.Path, "a.second.mkv"), 150)
	if now := bytesNowOf(t, s, l); now != 450 {
		t.Errorf("bytesNow %d, want 450: the original (300) and the newest output only (150)", now)
	}
}
