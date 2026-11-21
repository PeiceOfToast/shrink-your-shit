// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"reflect"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W1: the job's template is made from the workflow's resolved settings. A template stored as a build
// before W1 would have stored it (the reference came out as the setting's default) is not what the
// job runs: TemplateFor compiles the workflow's doc, so the quality the variable names is the one the
// plan has.

// qualityFlow is a published workflow whose Convert step's Quality is value, and a library using it.
func qualityFlow(t *testing.T, s *Store, variables []workflow.Variable, value any, stored rules.Template) (Library, Flow) {
	t.Helper()
	f, err := s.AddWorkflow("Typed", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = variables
	for i := range d.Doc.Nodes {
		if d.Doc.Nodes[i].Type == workflow.TypeConvert {
			d.Doc.Nodes[i].Config["quality"] = value
		}
	}
	saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(f.ID, saved.Etag, stored); err != nil {
		t.Fatal(err)
	}
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return lib, f
}

func TestTemplateForResolvesAWholeFieldReference(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := qualityFlow(t, s, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}}, "{{q}}", rules.SliderTemplate(3))
	got, err := s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := got.SelectorsView()
	if !ok || sel.Quality != 1 {
		t.Fatalf("the job's template has quality %+v (a selectors template: %v), want 1", sel.Quality, ok)
	}
}

// A workflow with no whole-field reference keeps the template it was published with, exactly: no
// recompile, so every existing workflow runs the plan it always had.
func TestTemplateForLeavesAWorkflowWithoutRefsAlone(t *testing.T) {
	useHome(t)
	s := open(t)
	stored := rules.SliderTemplate(2) // deliberately not the doc's quality (3): a recompile would say so
	lib, _ := qualityFlow(t, s, nil, float64(3), stored)
	got, err := s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("the template was rebuilt:\n got %+v\nwant %+v", got, stored)
	}
}
