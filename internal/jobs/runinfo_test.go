// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
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

// A job keeps the folder name it was made with, which need not be the one the library is stored under (a
// link, macOS's /var for /private/var, a Windows short name): the path lookup compares the real folder.
func TestRunInfoFindsTheLibraryUnderAnotherNameForItsFolder(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "movies-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	s := open(t)
	l, err := s.AddLibrary("Movies", real, rules.SliderTemplate(3), false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path == link {
		t.Fatalf("the library stored the link's own name (%q), so the test proves nothing", l.Path)
	}
	info, err := s.RunInfoFor(Job{Library: link})
	if err != nil {
		t.Fatal(err)
	}
	if info.Library != "Movies" || info.Workflow == "" {
		t.Errorf("by the folder's other name: %+v", info)
	}
}

// TemplateFor takes the same path, so such a job compiles the library's flow rather than "its library no
// longer exists".
func TestTemplateForFindsTheLibraryUnderAnotherNameForItsFolder(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "movies-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	s := open(t)
	if _, err := s.AddLibrary("Movies", real, rules.SliderTemplate(3), false); err != nil {
		t.Fatal(err)
	}
	tm, err := s.TemplateFor(Job{Library: link})
	if err != nil {
		t.Fatalf("TemplateFor by the folder's other name: %v", err)
	}
	if tm.Kind == "" {
		t.Errorf("the template is empty: %+v", tm)
	}
}
