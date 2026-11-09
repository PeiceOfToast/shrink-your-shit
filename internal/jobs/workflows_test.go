// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

// Migration 21 (V2): a job's history is written whatever path it takes, and stays after its row goes.
func TestJobHistory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetPaused(false)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	to := func(id int64) []string {
		t.Helper()
		ev, err := s.JobEvents(id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range ev {
			out = append(out, e.To)
		}
		return out
	}
	a, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	s.Claim("w1")
	s.RetryLater(a, time.Now().Add(-time.Second), "busy")
	s.Claim("w1")
	s.Complete(a, filepath.Join(l.Path, "a.mkv"), 1)
	if got := to(a); fmt.Sprint(got) != "[queued running queued running done]" {
		t.Errorf("a: %v", got)
	}
	b, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "b.mkv")})
	s.Skip(b, "it is already HEVC", rules.SkipAlreadyInFormat)
	c, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "c.mkv")})
	s.UserStopped(c)
	if got := to(b); fmt.Sprint(got) != "[queued skipped]" {
		t.Errorf("b: %v", got)
	}
	if ev, _ := s.JobEvents(c); len(ev) != 2 || ev[1].To != "cancelled" || ev[1].Reason != "user_stopped" {
		t.Errorf("c: %+v", ev)
	}
	if _, err := s.Get(b); err == nil {
		t.Error("a skipped job's row is still there (v1 behaviour kept)")
	}
	// the revision a job ran: the one published when it was queued
	rev, ok, err := s.RevisionAt(l.FlowID, time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil || !ok || *rev.Number != 1 {
		t.Errorf("revision %+v %v %v", rev, ok, err)
	}
}
