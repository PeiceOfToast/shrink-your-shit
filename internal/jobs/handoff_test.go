// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// gotoFlow is a published workflow named name whose files are handed to workflow target (0: a plain
// slider workflow with no hand-over), with a library using it.
func gotoFlow(t *testing.T, s *Store, name string, target int64, stored rules.Template) (Library, Flow) {
	t.Helper()
	f, err := s.AddWorkflow(name, workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target != 0 { // Library files → Hand to another workflow; the preset's steps are left unlinked and removed
		in := d.Doc.Nodes[0]
		g := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeGoto, TypeVersion: 1, Enabled: true, Config: map[string]any{"workflow": strconvI(target)}}
		d.Doc.Nodes = []workflow.Node{in, g}
		d.Doc.Edges = []workflow.Edge{{ID: workflow.NewID(), From: in.ID, FromPort: "success", To: g.ID, ToPort: "in"}}
		var issues []workflow.Issue
		stored, issues = workflow.ToTemplate(d.Doc, rules.Template{})
		if len(issues) > 0 {
			t.Fatalf("%+v", issues)
		}
	}
	saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(f.ID, saved.Etag, stored); err != nil {
		t.Fatal(err)
	}
	lib, err := s.AddLibraryOnFlow(name+" library", t.TempDir(), f.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return lib, f
}

func strconvI(n int64) string { return strings.TrimSpace(strings.Repeat(" ", 0) + itoa(n)) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// FB4b: a workflow that hands its files to another runs that workflow's paths. The scanner's and the
// worker's templates have the hand-over filled in (GetRunFlow, TemplateFor); GetFlow stays the template
// as published. A target that was never published, or is gone, ends the path as failed with words that
// say so; a loop between two workflows does the same. Validation's view of a workflow (OtherWorkflow)
// says whether it was published and where its paths hand files to.
func TestHandToAnotherWorkflowRunsItsPaths(t *testing.T) {
	useHome(t)
	s := open(t)
	_, plain := gotoFlow(t, s, "Plain", 0, rules.SliderTemplate(2))
	lib, a := gotoFlow(t, s, "Hands over", plain.ID, rules.Template{})
	film := media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}

	raw, err := s.GetFlow(a.ID)
	if err != nil || raw.Template.Branches == nil || (*raw.Template.Branches)[0].Goto != plain.ID {
		t.Fatalf("as published, the hand-over is a path of its own: %+v %v", raw.Template, err)
	}
	run, err := s.GetRunFlow(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b := (*run.Template.Branches)[0]; b.Goto != 0 || b.Then == nil || b.Then.Params.Quality != 2 || len(b.Handoffs) != 1 || b.Handoffs[0].Name != "Plain" {
		t.Fatalf("as run, the path is the other workflow's: %+v", b)
	}
	if p := run.Template.Compile(film); p.Skip != "" || p.Video.CRF != rules.SliderTemplate(2).Compile(film).Video.CRF {
		t.Errorf("the scanner's plan converts with Plain's steps: %+v", p)
	}
	got, err := s.TemplateFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Compile(film); p.Skip != "" || len(p.Decisions) != 1 || p.Decisions[0].Words != "Hand to another workflow → Plain" {
		t.Errorf("the worker's plan: %+v", p)
	}
	// what validation is told
	if o, ok := s.OtherWorkflow(a.ID); !ok || !o.Published || o.Name != "Hands over" || len(o.HandsTo) != 1 || o.HandsTo[0] != plain.ID {
		t.Errorf("OtherWorkflow: %+v %v", o, ok)
	}
	if _, ok := s.OtherWorkflow(a.ID + 99); ok {
		t.Error("a workflow that isn't there")
	}
	// a target never published: the path fails and says so
	draft, err := s.AddWorkflow("Still a draft", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := s.OtherWorkflow(draft.ID); !ok || o.Published {
		t.Errorf("a draft is not published: %+v", o)
	}
	_, b := gotoFlow(t, s, "To a draft", draft.ID, rules.Template{})
	if p := mustRun(t, s, b.ID).Compile(film); !p.Fail || !strings.Contains(p.Skip, "isn't published any more") {
		t.Errorf("a draft target: %+v", p)
	}
	// a loop: C hands to D, D hands to C (made by republishing D's template, as a later edit would)
	_, c := gotoFlow(t, s, "C", plain.ID, rules.Template{})
	_, d := gotoFlow(t, s, "D", c.ID, rules.Template{})
	tx, _ := s.db.Begin()
	if err := s.setTemplate(tx, c.ID, rules.Template{Kind: rules.KindBranches, Branches: &[]rules.Branch{{Goto: d.ID, GotoNode: "g", Path: []string{"in", "g"}}}}); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if p := mustRun(t, s, c.ID).Compile(film); !p.Fail || !strings.Contains(p.Skip, "round in a loop") {
		t.Errorf("a loop: %+v", p)
	}
}

func mustRun(t *testing.T, s *Store, id int64) rules.Template {
	t.Helper()
	f, err := s.GetRunFlow(id)
	if err != nil {
		t.Fatal(err)
	}
	return f.Template
}
