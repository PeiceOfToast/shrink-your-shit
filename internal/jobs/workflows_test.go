// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	s.RetryLater(a, time.Now().Add(-time.Second), "convert_failed", "busy")
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

// Review 66806b3: every way a job's row goes ends its history (a queued job stopped by hand, one
// removed with its library); history is pruned after the run logs' 30 days; a retry wait can go now;
// Claim's order has its index.
func TestJobHistoryEnds(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	last := func(id int64) JobEvent {
		t.Helper()
		ev, _ := s.JobEvents(id)
		if len(ev) == 0 {
			t.Fatalf("job %d has no history", id)
		}
		return ev[len(ev)-1]
	}
	stopped, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if _, err := s.RequestStop(stopped); err != nil {
		t.Fatal(err)
	}
	if e := last(stopped); e.To != "cancelled" || e.Reason != "user_stopped" {
		t.Errorf("stopped while waiting: %+v", e)
	}
	if ev, _ := s.JobEvents(stopped); len(ev) != 2 {
		t.Errorf("one end, not two: %+v", ev)
	}
	other, _ := s.AddLibrary("Shows", "/s", rules.SliderTemplate(3), true)
	gone, _ := s.Enqueue(Job{Library: other.Path, LibraryID: other.ID, Path: filepath.Join(other.Path, "b.mkv")})
	if err := s.DeleteLibrary(other.ID); err != nil {
		t.Fatal(err)
	}
	if e := last(gone); e.To != "cancelled" || e.Reason != "removed" {
		t.Errorf("removed with its library: %+v", e)
	}
	// retry now
	r, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "r.mkv")})
	s.SetPaused(false)
	s.Claim("w")
	s.RetryLater(r, time.Now().Add(time.Hour), "convert_failed", "busy")
	if err := s.RetryNow(r); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(r); j.RetryAt != "" {
		t.Errorf("still waiting to retry: %q", j.RetryAt)
	}
	if err := s.RetryNow(r); err == nil {
		t.Error("a job not waiting to retry can't retry now")
	}
	// pruned after 30 days
	s.db.Exec(`UPDATE events SET at='2000-01-01T00:00:00Z' WHERE kind='job' AND job_id=?`, stopped)
	if n, err := s.PruneJobEvents(time.Now().Add(-30 * 24 * time.Hour)); err != nil || n != 2 {
		t.Errorf("pruned %d %v", n, err)
	}
	// Claim's query uses its index
	var plan string
	rows, _ := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM jobs WHERE status='queued' AND stop_requested=0 ORDER BY priority DESC, id LIMIT 1`)
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan += d + "; "
	}
	rows.Close()
	if !strings.Contains(plan, "jobs_claim") {
		t.Errorf("plan %s", plan)
	}
}

// Migration 22 (V3a): job_events becomes events; the history keeps its seq (ids already sent stay
// valid) and new entries continue after it.
func TestEventsMigrationKeepsSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	for _, q := range []string{ // back to version 21's shape, with history at seq 41 and 42
		`DROP TRIGGER events_job_insert`, `DROP TRIGGER events_job_status`, `DROP TRIGGER events_job_removed`, `DROP TABLE events`,
		`CREATE TABLE job_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, job_id INTEGER NOT NULL, at TEXT NOT NULL,
		  from_status TEXT, to_status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', worker TEXT NOT NULL DEFAULT '')`,
		`CREATE TRIGGER job_events_insert AFTER INSERT ON jobs BEGIN SELECT 1; END`,
		`CREATE TRIGGER job_events_status AFTER UPDATE OF status ON jobs BEGIN SELECT 1; END`,
		`CREATE TRIGGER job_events_removed AFTER DELETE ON jobs BEGIN SELECT 1; END`,
		`INSERT INTO job_events(seq, job_id, at, to_status) VALUES (41, 7, '2026-11-01T00:00:00Z', 'queued'), (42, 7, '2026-11-01T00:00:01Z', 'running')`,
		`ALTER TABLE jobs DROP COLUMN error_code`, `DROP TABLE workers`, `ALTER TABLE libraries DROP COLUMN override_folder`, `ALTER TABLE libraries DROP COLUMN priority`, `ALTER TABLE jobs DROP COLUMN original_gone_at`, `ALTER TABLE jobs DROP COLUMN original_changed`, `ALTER TABLE jobs DROP COLUMN original_disk`, `ALTER TABLE jobs DROP COLUMN route`, `ALTER TABLE job_steps DROP COLUMN node_id`, `ALTER TABLE job_steps DROP COLUMN port`, `ALTER TABLE jobs DROP COLUMN fallback`, `ALTER TABLE jobs DROP COLUMN lease_id`, `ALTER TABLE jobs DROP COLUMN lease_expires`, `ALTER TABLE files DROP COLUMN job_id`, `ALTER TABLE jobs DROP COLUMN again_from`, `DROP TABLE worker_pairings`, // migrations 23 to 27 run again
		`PRAGMA user_version=21`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev, _ := s.JobEvents(7); len(ev) != 2 || ev[0].Seq != 41 || ev[1].Seq != 42 || ev[1].To != "running" {
		t.Errorf("old history %+v", ev)
	}
	id, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if ev, _ := s.JobEvents(id); len(ev) != 1 || ev[0].Seq <= 42 {
		t.Errorf("new entries continue the seq: %+v", ev)
	}
}

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

// Migration 23: history from before reason codes (the sentence as the code) gets a code from where the
// job went, the sentence as its message; a failed job's code is set; new changes record both.
func TestReasonCodesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	id, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(id, ReasonConvertFailed, "Converting did not finish."); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{ // back to version 22: the sentence was the code
		`UPDATE events SET reason='Converting did not finish.', data=NULL WHERE kind='job' AND to_status='failed'`,
		`INSERT INTO events(kind, job_id, at, from_status, to_status, reason) VALUES ('job', 99, '2026-11-01T00:00:00Z', 'running', 'cancelled', 'user_stopped')`,
		// the worker's own sentences: a low-space retry and a stall (review abe2b0e: not legacy retry_later)
		`INSERT INTO events(kind, job_id, at, from_status, to_status, reason) VALUES
		  ('job', 98, '2026-11-01T00:00:00Z', 'running', 'queued', 'there is not enough free disk space next to this file: about 2.0 GB is needed to convert it safely, 1.0 GB is free'),
		  ('job', 97, '2026-11-01T00:00:00Z', 'running', 'failed', 'Stopped: no progress for 10m0s. The original is untouched; press Retry to try again.'),
		  ('job', 96, '2026-11-01T00:00:00Z', 'running', 'queued', 'Something else that may pass.')`,
		`DROP TRIGGER events_job_status`, `DROP TRIGGER events_job_removed`,
		`CREATE TRIGGER events_job_status AFTER UPDATE OF status ON jobs BEGIN SELECT 1; END`,
		`CREATE TRIGGER events_job_removed AFTER DELETE ON jobs BEGIN SELECT 1; END`,
		`ALTER TABLE jobs DROP COLUMN error_code`, `DROP TABLE workers`, `ALTER TABLE libraries DROP COLUMN override_folder`, `ALTER TABLE libraries DROP COLUMN priority`, `ALTER TABLE jobs DROP COLUMN original_gone_at`, `ALTER TABLE jobs DROP COLUMN original_changed`, `ALTER TABLE jobs DROP COLUMN original_disk`, `ALTER TABLE jobs DROP COLUMN route`, `ALTER TABLE job_steps DROP COLUMN node_id`, `ALTER TABLE job_steps DROP COLUMN port`, `ALTER TABLE jobs DROP COLUMN fallback`, `ALTER TABLE jobs DROP COLUMN lease_id`, `ALTER TABLE jobs DROP COLUMN lease_expires`, `ALTER TABLE files DROP COLUMN job_id`, `ALTER TABLE jobs DROP COLUMN again_from`, `DROP TABLE worker_pairings`, `PRAGMA user_version=22`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev, _ := s.JobEvents(id)
	if last := ev[len(ev)-1]; last.Reason != ReasonConvertFailed || last.Message != "Converting did not finish." {
		t.Errorf("old failure %+v", last)
	}
	if ev, _ := s.JobEvents(99); len(ev) != 1 || ev[0].Reason != UserStoppedReason || ev[0].Message == "" {
		t.Errorf("old stop %+v", ev)
	}
	for job, want := range map[int64]string{98: ReasonLowDiskSpace, 97: ReasonStalled, 96: ReasonRetryLater} {
		if ev, _ := s.JobEvents(job); len(ev) != 1 || ev[0].Reason != want || ev[0].Message == "" {
			t.Errorf("old event of job %d: %+v, want %s", job, ev, want)
		}
	}
	var code string
	s.db.QueryRow(`SELECT error_code FROM jobs WHERE id=?`, id).Scan(&code)
	if code != ReasonConvertFailed {
		t.Errorf("the failed job's code %q", code)
	}
	s.Retry(id)
	s.Claim("w")
	s.RetryLater(id, time.Now(), ReasonLowDiskSpace, "there is not enough free disk space next to this file")
	ev, _ = s.JobEvents(id)
	if last := ev[len(ev)-1]; last.Reason != ReasonLowDiskSpace || last.Message != "there is not enough free disk space next to this file" {
		t.Errorf("new retry %+v", last)
	}
}
