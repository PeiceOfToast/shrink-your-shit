// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V5b: a paused library's waiting jobs are never taken (another library's are); its running job
// finishes; Start (also the old pages') resumes it.
func TestPausedLibraryIsNotClaimed(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	movies, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	tv, _ := s.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	add := func(l Library, name string) int64 {
		id, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, name)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	running := add(movies, "a.mkv")
	s.Claim("w")
	waiting := add(movies, "b.mkv")
	other := add(tv, "c.mkv")
	if err := s.StopLibrary(movies.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := s.Claim("w"); err != nil || j.ID != other {
		t.Fatalf("claimed %d %v, want only TV's %d", j.ID, err, other)
	}
	if _, err := s.Claim("w"); err != ErrNoJob {
		t.Errorf("a paused library's job was taken: %v", err)
	}
	if err := s.Complete(running, filepath.Join(movies.Path, "a.mkv"), 1); err != nil { // its running job finishes
		t.Errorf("running job: %v", err)
	}
	if _, err := s.Enqueue(Job{Library: movies.Path, LibraryID: movies.ID, Path: filepath.Join(movies.Path, "new.mkv")}); err != ErrLibraryGone {
		t.Errorf("queued into a paused library: %v", err)
	}
	if err := s.StartLibrary(movies.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := s.Claim("w"); err != nil || j.ID != waiting {
		t.Errorf("after Start: %d %v, want %d", j.ID, err, waiting)
	}
	if err := s.StopLibrary(999); err != ErrNotFound {
		t.Errorf("no such library: %v", err)
	}
}

// A library's priority goes to its waiting jobs and its new ones, so they are taken first.
func TestLibraryPriority(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	movies, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	tv, _ := s.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	m1, _ := s.Enqueue(Job{Library: movies.Path, LibraryID: movies.ID, Path: filepath.Join(movies.Path, "m1.mkv")})
	t1, _ := s.Enqueue(Job{Library: tv.Path, LibraryID: tv.ID, Path: filepath.Join(tv.Path, "t1.mkv")})
	if err := s.SetLibraryPriority(tv.ID, 5); err != nil {
		t.Fatal(err)
	}
	t2, _ := s.Enqueue(Job{Library: tv.Path, LibraryID: tv.ID, Path: filepath.Join(tv.Path, "t2.mkv")})
	if l, _ := s.GetLibrary(tv.ID); l.Priority != 5 {
		t.Errorf("priority %d", l.Priority)
	}
	for _, want := range []int64{t1, t2, m1} {
		if j, err := s.Claim("w"); err != nil || j.ID != want {
			t.Fatalf("claimed %d %v, want %d", j.ID, err, want)
		}
	}
}
