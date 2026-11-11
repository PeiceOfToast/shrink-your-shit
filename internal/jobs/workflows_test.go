// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// Migration 20 (V1): every flow gets revision 1 when the store opens, a flow saved by the old pages
// gets the next revision (same node ids), and the workflow's draft, publish and history work.
func TestWorkflowRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r1, ok, err := s.LatestRevision(l.FlowID)
	if err != nil || !ok || *r1.Number != 1 || r1.Status != "published" || len(r1.Doc.Nodes) != 8 {
		t.Fatalf("revision 1: %+v %v %v", r1, ok, err)
	}
	if again, _ := s.EnsureRevision(l.FlowID); again.ID != r1.ID {
		t.Errorf("nothing changed, yet a new revision: %+v", again)
	}
	// the old pages save the flow: revision 2, same ids
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(4)); err != nil {
		t.Fatal(err)
	}
	r2, err := s.EnsureRevision(l.FlowID)
	if err != nil || *r2.Number != 2 || r2.Doc.Nodes[0].ID != r1.Doc.Nodes[0].ID || r2.LogicHash == r1.LogicHash {
		t.Fatalf("revision 2: %+v %v", r2, err)
	}
	// a draft: made from the newest revision, saved with its etag, refused with a stale one
	d, fresh, err := s.Draft(l.FlowID)
	if err != nil || !fresh || d.Etag != r2.Etag {
		t.Fatalf("draft %+v %v %v", d, fresh, err)
	}
	doc := d.Doc
	doc.Nodes[2].Config["quality"] = 5
	saved, err := s.SaveDraft(l.FlowID, doc, workflow.Layout{}, d.Etag)
	if err != nil || saved.Status != "draft" || saved.Number != nil {
		t.Fatalf("save %+v %v", saved, err)
	}
	if _, err := s.SaveDraft(l.FlowID, doc, workflow.Layout{}, d.Etag); !errors.Is(err, ErrStaleDraft) {
		t.Errorf("stale save: %v", err)
	}
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(4))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	r3, err := s.Publish(l.FlowID, saved.Etag, tm)
	if err != nil || *r3.Number != 3 {
		t.Fatalf("publish %+v %v", r3, err)
	}
	if f, _ := s.GetFlow(l.FlowID); f.Template.Selectors == nil || f.Template.Selectors.Quality != 5 {
		t.Errorf("the flow runs %+v", f.Template)
	}
	if _, fresh, _ := s.Draft(l.FlowID); !fresh {
		t.Error("the draft is still there after publishing")
	}
	if v, _ := s.EnsureRevision(l.FlowID); v.ID != r3.ID {
		t.Errorf("publishing's own template made another revision: %+v", v)
	}
	if revs, _ := s.Revisions(l.FlowID); len(revs) != 3 || *revs[0].Number != 3 {
		t.Errorf("history %d", len(revs))
	}
	// a new workflow: never published, so the old pages don't list it; deleting it takes its revisions
	w, err := s.AddWorkflow("Fresh", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.WorkflowMetaOf(w.ID); m.Status != "draft" {
		t.Errorf("status %q", m.Status)
	}
	uses, _ := s.Flows()
	for _, u := range uses {
		if u.ID == w.ID {
			t.Error("a never-published workflow is listed for the old pages")
		}
	}
	if _, _, err := s.Draft(w.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFlow(w.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_revisions WHERE workflow_id=?`, w.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d revisions left", n)
	}
}

// Review 109: a flow that can't be read doesn't stop the store opening (the app starting); the others
// still get their revision.
func TestOpenSurvivesABadFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := s.AddLibrary("Good", "/g", rules.SliderTemplate(3), true)
	bad, _ := s.AddLibrary("Bad", "/b", rules.SliderTemplate(3), true)
	s.db.Exec(`DELETE FROM workflow_revisions`)
	s.db.Exec(`UPDATE flows SET template='not json' WHERE id=?`, bad.FlowID)
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatalf("one bad flow stopped Open: %v", err)
	}
	defer s.Close()
	if _, ok, _ := s.LatestRevision(good.FlowID); !ok {
		t.Error("the good flow got no revision")
	}
}
