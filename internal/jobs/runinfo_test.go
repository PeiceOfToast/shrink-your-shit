// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// RunInfoFor names the library and its workflow for a job, by library id or by path; a library that is gone
// is not an error, its values are just empty (the run still knows its own file).
func TestRunInfoFor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil { // opening gives the flow its first revision (migration 20)
		t.Fatal(err)
	}
	defer s.Close()
	byID, err := s.RunInfoFor(Job{LibraryID: l.ID, Library: l.Path})
	if err != nil {
		t.Fatal(err)
	}
	if byID.Library != "Movies" || byID.Workflow == "" || byID.Revision != 1 {
		t.Errorf("by id: %+v", byID)
	}
	// a job with no library id falls back to the path
	byPath, err := s.RunInfoFor(Job{Library: l.Path})
	if err != nil {
		t.Fatal(err)
	}
	if byPath.Library != byID.Library || byPath.Workflow != byID.Workflow || byPath.Revision != byID.Revision {
		t.Errorf("by path: %+v, want %+v", byPath, byID)
	}
	if err := s.DeleteLibrary(l.ID); err != nil {
		t.Fatal(err)
	}
	gone, err := s.RunInfoFor(Job{LibraryID: l.ID, Library: l.Path})
	if err != nil || gone.Library != "" || gone.Workflow != "" || gone.Revision != 0 {
		t.Errorf("a removed library: %+v, %v", gone, err)
	}
}
