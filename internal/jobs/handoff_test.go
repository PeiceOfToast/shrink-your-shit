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

// review-579 P1: a source whose typed settings name a variable is compiled again from its document for
// each file (the scanner's planFor, the worker's resolveForFile); the hand-over must survive that, or the
// file is skipped as "handed to another workflow" and never queued. And the scope rule: a target whose
// typed settings name a global reads the global's value as it is now.
func TestHandOverSurvivesAPerFileResolveAndReadsTheTargetsCurrentValues(t *testing.T) {
	useHome(t)
	s := open(t)
	film := media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, VideoCodec: "h264", Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}
	// the target: its Convert step's quality is the global {{g}}
	if _, err := s.SetGlobalVariables([]workflow.Variable{{Name: "g", Type: "number", Value: float64(4)}}); err != nil {
		t.Fatal(err)
	}
	_, target := qualityFlow(t, s, nil, "{{g}}", rules.SliderTemplate(1)) // stored with quality 1: a stale template, on purpose
	// the source: its own typed variable {{q}}, and every file handed to the target
	srcLib, src := gotoFlow(t, s, "Typed source", target.ID, rules.Template{})
	d, _, _ := s.Draft(src.ID)
	d.Doc.Variables = []workflow.Variable{{Name: "q", Type: "number", Value: float64(2)}}
	saved, err := s.SaveDraft(src.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	// a typed reference somewhere in the source: a Convert step is not on this path, so put one on a second path
	// through a question; the hand-over path stays the first
	doc := saved.Doc
	q := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeCheckName, TypeVersion: 1, Enabled: true, Config: map[string]any{"pattern": "keep", "match": "contains"}}
	conv := workflow.FromTemplate(rules.SliderTemplate(3), nil).Nodes[1:] // the preset's converting steps
	for i := range conv {
		if conv[i].Type == workflow.TypeConvert {
			conv[i].Config["quality"] = "{{q}}"
		}
	}
	in := doc.Nodes[0]
	goTo := doc.Nodes[1]
	doc.Nodes = append([]workflow.Node{in, q, goTo}, conv...)
	doc.Edges = []workflow.Edge{{ID: workflow.NewID(), From: in.ID, FromPort: "success", To: q.ID, ToPort: "in"},
		{ID: workflow.NewID(), From: q.ID, FromPort: "match", To: conv[0].ID, ToPort: "in"},
		{ID: workflow.NewID(), From: q.ID, FromPort: "else", To: goTo.ID, ToPort: "in"}}
	for i := 1; i < len(conv); i++ {
		doc.Edges = append(doc.Edges, workflow.Edge{ID: workflow.NewID(), From: conv[i-1].ID, FromPort: "success", To: conv[i].ID, ToPort: "in"})
	}
	tmpl, issues := workflow.ToTemplate(doc, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%+v", issues)
	}
	saved2, err := s.SaveDraft(src.ID, doc, saved.Layout, saved.Etag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(src.ID, saved2.Etag, tmpl); err != nil {
		t.Fatal(err)
	}
	if !workflow.HasRefs(doc) {
		t.Fatal("the source should carry a typed reference")
	}
	// the worker's template, then the per-file resolve the worker and the scanner do
	got, err := s.TemplateFor(Job{LibraryID: srcLib.ID, Path: "/films/Other.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	g, _ := s.GlobalVariables()
	sc := workflow.Scopes{Global: workflow.VariableMap(g), File: workflow.FileValues("/films/Other.mkv", film, film.SizeBytes)}
	info, _ := s.RunInfoFor(Job{LibraryID: srcLib.ID})
	resolved, iss := workflow.ToTemplateWith(info.Doc, got, sc)
	if len(iss) > 0 {
		t.Fatalf("%+v", iss)
	}
	p := resolved.CompileWith(film, rules.Facts{Path: "/films/Other.mkv"})
	if p.Skip != "" || len(p.Decisions) != 2 || p.Decisions[1].Words != "Hand to another workflow → Typed" {
		t.Fatalf("after the per-file resolve the hand-over must still run: %+v", p)
	}
	// the target's current value: its quality is the global's 4, not the stored template's 1
	if want := rules.SliderTemplate(4).Compile(film).Video.CRF; p.Video.CRF != want {
		t.Errorf("the target's quality should follow the global (CRF %d), got %d", want, p.Video.CRF)
	}
	if _, err := s.SetGlobalVariables([]workflow.Variable{{Name: "g", Type: "number", Value: float64(5)}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.TemplateFor(Job{LibraryID: srcLib.ID, Path: "/films/Other.mkv"})
	if p := got.CompileWith(film, rules.Facts{Path: "/films/Other.mkv"}); p.Video.CRF != rules.SliderTemplate(5).Compile(film).Video.CRF {
		t.Errorf("a changed global reaches the target: %+v", p.Video)
	}
}
