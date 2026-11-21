// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W2 (workflow-architecture.md 2.3): the server's global variables are read when a job starts, so a
// save touches the next job's plan without a publish; it counts the workflows and libraries whose
// published settings name a changed global, and forgets no file.

// globalFlow is a published workflow whose Convert step's Quality is value, with no library.
func globalFlow(t *testing.T, s *Store, name string, value any) Flow {
	t.Helper()
	f, err := s.AddWorkflow(name, workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Doc.Nodes {
		if d.Doc.Nodes[i].Type == workflow.TypeConvert {
			d.Doc.Nodes[i].Config["quality"] = value
		}
	}
	saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(f.ID, saved.Etag, rules.SliderTemplate(3)); err != nil {
		t.Fatal(err)
	}
	return f
}

// Saving a global counts the workflows that name it and the libraries that use them, before and as
// it saves. A workflow that doesn't name a changed global is not counted.
func TestSavingAGlobalSaysWhichWorkflowsAndLibrariesItTouches(t *testing.T) {
	useHome(t)
	s := open(t)
	q1 := globalFlow(t, s, "First", "{{q}}")
	q2 := globalFlow(t, s, "Second", "{{q}}")
	plain := globalFlow(t, s, "Third", float64(3))
	for _, f := range []Flow{q1, q1, q2, plain} {
		if _, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	next := []workflow.Variable{{Name: "q", Type: "number", Value: float64(2)}}
	want := GlobalAffects{Workflows: []int64{q1.ID, q2.ID}, Libraries: 3}
	preview, err := s.GlobalAffectsOf(next)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview, want) {
		t.Errorf("the preview says %+v, want %+v", preview, want)
	}
	got, err := s.SetGlobalVariables(next)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the save says %+v, want %+v", got, want)
	}
	saved, err := s.GlobalVariables()
	if err != nil || !reflect.DeepEqual(saved, next) {
		t.Errorf("saved %+v, %v; want %+v", saved, err, next)
	}
	// saving the same values again touches nothing
	if again, err := s.GlobalAffectsOf(next); err != nil || len(again.Workflows) != 0 || again.Libraries != 0 {
		t.Errorf("an unchanged save says %+v, %v; want nothing", again, err)
	}
}

// Saving a global forgets no library's files: there is no scan to queue, because the next job reads
// the globals itself.
func TestSavingAGlobalForgetsNoFiles(t *testing.T) {
	useHome(t)
	s := open(t)
	f := globalFlow(t, s, "Global", "{{q}}")
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib.Path, "Cat.mkv")
	if err := s.PutFile(File{Path: path, Size: 1234, ModTime: 99}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGlobalVariables([]workflow.Variable{{Name: "q", Type: "number", Value: float64(2)}}); err != nil {
		t.Fatal(err)
	}
	file, err := s.GetFile(path)
	if err != nil || file == nil {
		t.Fatalf("the file row is gone after a global save: %v, %v", file, err)
	}
	if file.Size != 1234 || file.ModTime != 99 {
		t.Errorf("the file row is %+v, want it as it was", file)
	}
	if jobs, err := s.List(10); err != nil || len(jobs) != 0 {
		t.Errorf("the save queued %d jobs, %v; want none", len(jobs), err)
	}
}

// A job's template reads the globals as they are when it starts: saving a global changes the next
// job's plan without publishing the workflow again.
func TestTemplateForReadsTheGlobalsAsTheyAreNow(t *testing.T) {
	useHome(t)
	s := open(t)
	f := globalFlow(t, s, "Global", "{{q}}")
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []int{2, 4} {
		if _, err := s.SetGlobalVariables([]workflow.Variable{{Name: "q", Type: "number", Value: float64(q)}}); err != nil {
			t.Fatal(err)
		}
		got, err := s.TemplateFor(Job{LibraryID: lib.ID})
		if err != nil {
			t.Fatal(err)
		}
		sel, ok := got.SelectorsView()
		if !ok || sel.Quality != q {
			t.Errorf("after saving q=%d the job's template has quality %+v (a selectors template: %v), want %d", q, sel.Quality, ok, q)
		}
	}
}
