// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// Foundation slice 9: a job records the workflow revision it was queued under (queued_rev) and the one it
// actually ran with (workflow_rev, stamped when a worker reads the workflow); a way back to the queue changes
// neither.

// startStamped queues a job, claims it and records the revision it starts with (revision B: the library's
// template as it is now). It returns the store, the job and B's id.
func startStamped(t *testing.T, template int) (*Store, int64, int64) {
	t.Helper()
	s := open(t)
	l, err := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(template), true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRunRevision(id, 0, rules.SliderTemplate(template)); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.LatestRevision(l.FlowID)
	if err != nil || !ok {
		t.Fatalf("latest revision %v %v", ok, err)
	}
	return s, id, b.ID
}

// The main case: a job queued under revision A and started after B was published records B as what it ran
// with and A as what it was queued under, so it is current, not "earlier", to Convert again.
func TestAJobRecordsTheRevisionItRanWith(t *testing.T) {
	s := open(t)
	l, err := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(l.Path, "a.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: path, SizeBefore: 8})
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.LatestRevision(l.FlowID)
	if err != nil || !ok {
		t.Fatalf("the queued revision %v %v", ok, err)
	}
	// the workflow changes while the job waits
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(6)); err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureRevision(l.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	// a worker claims it and records the revision it read
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	tmpl, rev, err := s.TemplateFor(Job{Library: l.Path, LibraryID: l.ID, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRunRevision(id, rev, tmpl); err != nil {
		t.Fatal(err)
	}
	ran, queued, err := s.JobRevisions(id)
	if err != nil {
		t.Fatal(err)
	}
	if ran != b.ID || queued != a.ID {
		t.Errorf("ran %d, queued %d; want %d (B) and %d (A)", ran, queued, b.ID, a.ID)
	}
	// it finished with B: current, so there is nothing to convert again
	if err := s.Complete(id, filepath.Join(l.Path, "new-a.mkv"), 8); err != nil {
		t.Fatal(err)
	}
	if c, err := s.ConvertAgainCheck(id, 7); err != nil || c.Possible || c.Code != AgainCurrent {
		t.Errorf("converted with the current workflow: %+v %v", c, err)
	}
	// a Convert again is a new queueing: it is queued under the newest revision, and runs with it
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(8)); err != nil {
		t.Fatal(err)
	}
	c, err := s.EnsureRevision(l.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConvertAgain(id); err != nil {
		t.Fatal(err)
	}
	if ran, queued, err = s.JobRevisions(id); err != nil || ran != c.ID || queued != c.ID {
		t.Errorf("converted again: ran %d, queued %d; want %d for both (%v)", ran, queued, c.ID, err)
	}
}

// A Save that has no revision yet gets one when a worker records its run revision, and the job records it.
// The old pages' SaveTemplate writes the flow's template and no revision (SetFlowTemplate makes one itself,
// so it would not reach this branch; review2-593 M9).
func TestRecordedRunRevisionWritesARevisionForASaveWithoutOne(t *testing.T) {
	s, id, b := startStamped(t, 4)
	j, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveTemplate(j.LibraryID, rules.SliderTemplate(6), ""); err != nil {
		t.Fatal(err)
	}
	if latest, ok, err := s.LatestRevision(lFlowOf(t, s, id)); err != nil || !ok || latest.ID != b {
		t.Fatalf("SaveTemplate made a revision (%+v %v %v); the branch it tests is not reached", latest, ok, err)
	}
	if err := s.RecordRunRevision(id, 0, rules.SliderTemplate(6)); err != nil {
		t.Fatal(err)
	}
	ran, queued, err := s.JobRevisions(id)
	if err != nil {
		t.Fatal(err)
	}
	latest, ok, err := s.LatestRevision(lFlowOf(t, s, id))
	if err != nil || !ok {
		t.Fatalf("latest %v %v", ok, err)
	}
	if latest.ID == b || latest.Number == nil || *latest.Number != 2 {
		t.Errorf("a new revision was not written: %+v", latest)
	}
	if ran != latest.ID || queued != b {
		t.Errorf("ran %d, queued %d; want the new revision %d and the queued one %d", ran, queued, latest.ID, b)
	}
}

// lFlowOf is job id's library's flow.
func lFlowOf(t *testing.T, s *Store, id int64) int64 {
	t.Helper()
	j, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.GetLibrary(j.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	return l.FlowID
}

// A workflow whose typed settings name a global is compiled from its published document with the values
// as they are now: that document's revision is what the job runs with, recorded when the template is read.
// A publish while the job waits moves the record to the revision the settings came from, and the template
// itself cannot name it — it matches no stored revision (review-593 findings 2 and 3).
func TestARefsWorkflowRecordsTheRevisionItsSettingsCameFrom(t *testing.T) {
	useHome(t)
	s := open(t)
	f := globalFlow(t, s, "Global", "{{q}}")
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGlobalVariables([]workflow.Variable{{Name: "q", Type: "number", Value: float64(2)}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib.Path, "a.mkv")
	id, err := s.Enqueue(Job{Library: lib.Path, LibraryID: lib.ID, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	one, ok, err := s.LatestRevision(f.ID)
	if err != nil || !ok || one.Number == nil || *one.Number != 1 {
		t.Fatalf("the queued revision %+v %v %v", one, ok, err)
	}
	// version 2 is published while the job waits
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Publish(f.ID, saved.Etag, rules.SliderTemplate(3))
	if err != nil {
		t.Fatal(err)
	}
	if two.Number == nil || *two.Number != 2 || !workflow.HasRefs(two.Doc) {
		t.Fatalf("revision 2 is %+v, want a published one whose document names the global", two)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	tmpl, rev, err := s.TemplateFor(Job{Library: lib.Path, LibraryID: lib.ID, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if rev != two.ID {
		t.Fatalf("the template came from revision %d, want %d: a settings-compiled template matches no stored revision", rev, two.ID)
	}
	if err := s.RecordRunRevision(id, rev, tmpl); err != nil {
		t.Fatal(err)
	}
	ran, queued, err := s.JobRevisions(id)
	if err != nil || ran != two.ID || queued != one.ID {
		t.Errorf("ran %d, queued %d; want %d (version 2) and %d (version 1) (%v)", ran, queued, two.ID, one.ID, err)
	}
}

// A workflow that hands its files to another hands the worker an expanded template: no stored revision
// has that template's hash, and its document has branches rather than typed-setting references, so the
// old fallback left the queue-time revision. The revision is the one the flow's published template is, so
// a republish while the job waits is recorded as the revision it read (review2-593 P2).
func TestAHandOverWorkflowRecordsTheRevisionItRead(t *testing.T) {
	useHome(t)
	s := open(t)
	_, plain := gotoFlow(t, s, "Plain", 0, rules.SliderTemplate(2))
	lib, a := gotoFlow(t, s, "Hands over", plain.ID, rules.Template{}) // revision 1
	if err := s.StartLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib.Path, "a.mkv")
	id, err := s.Enqueue(Job{Library: lib.Path, LibraryID: lib.ID, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	one, ok, err := s.LatestRevision(a.ID)
	if err != nil || !ok || one.Number == nil || *one.Number != 1 {
		t.Fatalf("the queued revision %+v %v %v", one, ok, err)
	}
	// republished while the job waits: revision 2, still a hand-over, with a changed path
	d, _, err := s.Draft(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveDraft(a.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetFlow(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := raw.Template
	(*changed.Branches)[0].End = "Hand to another workflow: Plain (changed)"
	two, err := s.Publish(a.ID, saved.Etag, changed)
	if err != nil {
		t.Fatal(err)
	}
	if two.Number == nil || *two.Number != 2 {
		t.Fatalf("revision 2 is %+v", two)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	tmpl, rev, err := s.TemplateFor(Job{Library: lib.Path, LibraryID: lib.ID, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Branches == nil || (*tmpl.Branches)[0].Goto != 0 {
		t.Fatalf("the run template is not the expanded hand-over: %+v", tmpl)
	}
	if rev != two.ID {
		t.Fatalf("TemplateFor names revision %d, want the one it read: %d", rev, two.ID)
	}
	if err := s.RecordRunRevision(id, rev, tmpl); err != nil {
		t.Fatal(err)
	}
	ran, queued, err := s.JobRevisions(id)
	if err != nil || ran != two.ID || queued != one.ID {
		t.Errorf("ran %d, queued %d; want %d (the revision it read) and %d (queued) (%v)", ran, queued, two.ID, one.ID, err)
	}
}

// A way back to the queue (a retry, a crashed process, a lost lease, a release) keeps the revision the job
// ran with; the next start stamps it again. The workflow moves to C after B was recorded.
func TestARequeueKeepsTheRevisionTheJobRanWith(t *testing.T) {
	changeWorkflow := func(t *testing.T, s *Store, id int64) {
		t.Helper()
		if _, err := s.SetFlowTemplate(lFlowOf(t, s, id), rules.SliderTemplate(6)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EnsureRevision(lFlowOf(t, s, id)); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, s *Store, id, want int64) {
		t.Helper()
		ran, queued, err := s.JobRevisions(id)
		if err != nil {
			t.Fatal(err)
		}
		if ran != want || queued != want {
			t.Errorf("ran %d, queued %d; want %d for both", ran, queued, want)
		}
	}
	t.Run("requeue running", func(t *testing.T) {
		s, id, b := startStamped(t, 4)
		changeWorkflow(t, s, id)
		if n, err := s.RequeueRunning(); err != nil || n != 1 {
			t.Fatalf("requeued %d %v", n, err)
		}
		check(t, s, id, b)
	})
	t.Run("expired lease", func(t *testing.T) {
		s, id, b := startStamped(t, 4)
		changeWorkflow(t, s, id)
		lease, _ := s.LeaseOf(id)
		old := LeaseFor
		LeaseFor = -time.Second
		s.RenewLease(id, lease)
		LeaseFor = old
		if n, err := s.RequeueExpiredLeases(time.Minute); err != nil || n != 1 {
			t.Fatalf("requeued %d %v", n, err)
		}
		check(t, s, id, b)
	})
	t.Run("release", func(t *testing.T) {
		s, id, b := startStamped(t, 4)
		changeWorkflow(t, s, id)
		if err := s.Release(id); err != nil {
			t.Fatal(err)
		}
		check(t, s, id, b)
	})
	t.Run("retry", func(t *testing.T) {
		s, id, b := startStamped(t, 4)
		if err := s.Fail(id, "boom", "failed under revision B"); err != nil {
			t.Fatal(err)
		}
		changeWorkflow(t, s, id)
		if err := s.Retry(id); err != nil {
			t.Fatal(err)
		}
		check(t, s, id, b)
	})
}

// An upgraded database's jobs were queued under the revision they ran with: queued_rev is back-filled from
// workflow_rev, so no job shows the workflow as changed since.
func TestQueuedRevisionIsBackfilledFromTheRunRevision(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE jobs DROP COLUMN queued_rev`, `ALTER TABLE jobs DROP COLUMN output_info`, `PRAGMA user_version=58`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ran, queued, err := s.JobRevisions(id)
	if err != nil {
		t.Fatal(err)
	}
	if ran == 0 || queued != ran {
		t.Errorf("ran %d, queued %d; want the run revision for both", ran, queued)
	}
}
