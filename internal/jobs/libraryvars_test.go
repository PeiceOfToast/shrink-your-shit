// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W3 (workflow-architecture.md 2.2 rule 1): a library's own values for the variables its workflow lets
// a library change (templateParam). They live on the library row, a job reads them when it starts, and
// saving them forgets no files.

// A job's template takes the library's value for a variable the workflow lets a library change; the
// workflow's own value fills the setting until the library sets one.
func TestTemplateForReadsTheLibrarysValuesAsTheyAreNow(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := qualityFlow(t, s, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1), TemplateParam: true}},
		"{{q}}", rules.SliderTemplate(1))
	got, _, err := s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sel, ok := got.SelectorsView(); !ok || sel.Quality != 1 {
		t.Errorf("with no library value the template has quality %+v (a selectors template: %v), want the workflow's 1", sel.Quality, ok)
	}
	if err := s.SetLibraryVariables(lib.ID, []workflow.Variable{{Name: "q", Value: float64(4)}}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sel, ok := got.SelectorsView(); !ok || sel.Quality != 4 {
		t.Errorf("after the library's save the template has quality %+v (a selectors template: %v), want 4", sel.Quality, ok)
	}
	l, err := s.GetLibrary(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []workflow.Variable{{Name: "q", Value: float64(4)}}; !reflect.DeepEqual(l.Variables, want) {
		t.Errorf("the library reads back %+v, want %+v", l.Variables, want)
	}
}

// A library value on a variable the workflow doesn't let it change is not used: the workflow's own
// value runs. (The web layer refuses saving one; this is what a row written some other way does.)
func TestAWorkflowsOwnValueWinsWhereTheLibraryMayNotChangeIt(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := qualityFlow(t, s, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}},
		"{{q}}", rules.SliderTemplate(1))
	if err := s.SetLibraryVariables(lib.ID, []workflow.Variable{{Name: "q", Value: float64(4)}}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sel, ok := got.SelectorsView(); !ok || sel.Quality != 1 {
		t.Errorf("the template has quality %+v (a selectors template: %v), want the workflow's 1", sel.Quality, ok)
	}
}

// Saving a library's values forgets no file and queues nothing: the next job reads them itself.
func TestSavingALibrarysValuesForgetsNoFiles(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := qualityFlow(t, s, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1), TemplateParam: true}},
		"{{q}}", rules.SliderTemplate(1))
	path := filepath.Join(lib.Path, "Cat.mkv")
	if err := s.PutFile(File{Path: path, Size: 1234, ModTime: 99}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryVariables(lib.ID, []workflow.Variable{{Name: "q", Value: float64(3)}}); err != nil {
		t.Fatal(err)
	}
	file, err := s.GetFile(path)
	if err != nil || file == nil {
		t.Fatalf("the file row is gone after a library save: %v, %v", file, err)
	}
	if file.Size != 1234 || file.ModTime != 99 {
		t.Errorf("the file row is %+v, want it as it was", file)
	}
	if jobs, err := s.List(10); err != nil || len(jobs) != 0 {
		t.Errorf("the save queued %d jobs, %v; want none", len(jobs), err)
	}
	if err := s.SetLibraryVariables(lib.ID+1000, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("saving for a library that isn't there: %v, want ErrNotFound", err)
	}
}

// Migration 58 adds the column to an install from before W3: every library row keeps what it had, and
// uses its workflow's own values (no override).
func TestMigration58AddsTheLibraryVariablesKeepingTheRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := globalFlow(t, s, "Global", float64(3))
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryVariables(lib.ID, []workflow.Variable{{Name: "q", Value: float64(2)}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// back to version 57: as an install from before W3 left it
	db := rawDB(t, path)
	for _, q := range []string{`ALTER TABLE libraries DROP COLUMN variables`, `ALTER TABLE jobs DROP COLUMN queued_rev`, `PRAGMA user_version=57`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetLibrary(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != lib.Name || got.Path != lib.Path || got.FlowID != lib.FlowID || !got.Started || got.Created != lib.Created {
		t.Errorf("the row changed: %+v, want %+v", got, lib)
	}
	if len(got.Variables) != 0 {
		t.Errorf("the upgraded library has values %+v, want none", got.Variables)
	}
}
