// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// convertedIn makes a converted file in library l: its job is done with its output beside it, and the file
// stays (or is removed, for gone).
func convertedIn(t *testing.T, s *Store, l Library, name string, gone bool) int64 {
	t.Helper()
	path := filepath.Join(l.Path, name)
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: path, SizeBefore: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(id, filepath.Join(l.Path, "new-"+name), 4); err != nil {
		t.Fatal(err)
	}
	if gone {
		os.Remove(path)
	}
	return id
}

// Migration 51 stamps every existing job with its library's current workflow revision, so an upgraded install
// has no earlier files until its workflow changes.
func TestJobsStampCurrentRevisionOnUpgrade(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	cur, err := s.currentRevision(l.ID)
	if err != nil || cur == 0 {
		t.Fatalf("current revision %d %v", cur, err)
	}
	for _, q := range []string{`ALTER TABLE jobs DROP COLUMN subtitle_files`, `ALTER TABLE jobs DROP COLUMN workflow_rev`, `PRAGMA user_version=50`} {
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
	var rev int64
	if err := s.db.QueryRow(`SELECT workflow_rev FROM jobs WHERE id=?`, id).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if rev != cur {
		t.Errorf("stamped %d, want the library's current revision %d", rev, cur)
	}
}

// A flow's first revision backfills the jobs queued before it (workflow_rev 0): they ran with it.
func TestFirstRevisionBackfillsJobsQueuedBeforeIt(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	id, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")})
	for _, q := range []string{`DELETE FROM workflow_revisions WHERE workflow_id=` + strconv.FormatInt(l.FlowID, 10), `UPDATE jobs SET workflow_rev=0`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	r, err := s.EnsureRevision(l.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	var rev int64
	s.db.QueryRow(`SELECT workflow_rev FROM jobs WHERE id=?`, id).Scan(&rev)
	if rev != r.ID {
		t.Errorf("job stamped %d, want the first revision %d", rev, r.ID)
	}
}

// Changing the workflow makes the files converted with the old one earlier; a file converted with the new one is
// never counted, queued or offered; a second bulk Convert again queues nothing new.
func TestWorkflowChangeMakesEarlierFilesConvertAgain(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	a := convertedIn(t, s, l, "a.mkv", false)
	gone := convertedIn(t, s, l, "gone.mkv", true)

	if got, _ := s.LibraryAgain(l.ID, 7); got.Earlier != 0 {
		t.Fatalf("before any change: earlier %d", got.Earlier)
	}
	// the old pages save the flow with a new template: a new revision
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(6)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureRevision(l.FlowID); err != nil {
		t.Fatal(err)
	}
	// converted after the change: current
	cur := convertedIn(t, s, l, "current.mkv", false)

	got, err := s.LibraryAgain(l.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Earlier != 2 || got.Possible != 1 || len(got.NotPossible) != 1 || got.NotPossible[0].Code != AgainOriginalGone || got.NotPossible[0].Count != 1 {
		t.Fatalf("earlier %+v", got)
	}
	if c, _ := s.ConvertAgainCheck(cur, 7); c.Possible || c.Code != AgainCurrent {
		t.Errorf("per file, current: %+v", c)
	}
	if err := s.ConvertAgain(cur); !isAgainError(err, AgainCurrent) {
		t.Errorf("POST of a current file: %v", err)
	}
	if c, _ := s.ConvertAgainCheck(a, 7); !c.Possible {
		t.Errorf("per file, earlier: %+v", c)
	}

	queued, refused, err := s.ConvertLibraryAgain(l.ID, 7)
	if err != nil || queued != 1 || len(refused) != 1 || refused[0].Count != 1 {
		t.Fatalf("bulk: queued %d refused %+v %v", queued, refused, err)
	}
	if j, _ := s.Get(a); j.Status != Queued {
		t.Errorf("the possible file is %s, not queued", j.Status)
	}
	if j, _ := s.Get(cur); j.Status != Done {
		t.Errorf("the current file was touched: %s", j.Status)
	}
	if queued, refused, err := s.ConvertLibraryAgain(l.ID, 7); err != nil || queued != 0 || len(refused) != 1 {
		t.Errorf("second bulk: queued %d refused %+v %v", queued, refused, err)
	}

	// the run finishes with the current workflow: the file is current now, and so only the gone one is earlier
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(a, filepath.Join(l.Path, "new-a.mkv"), 4); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LibraryAgain(l.ID, 7); got.Earlier != 1 || got.Possible != 0 {
		t.Errorf("after the run: %+v (the gone file %d stays earlier)", got, gone)
	}
}

// ui-shop QA of Convert again: after a library's folder is changed, the files converted under the old folder
// are not counted or queued; only those inside the current folder are.
func TestConvertAgainOnlyInsideTheCurrentFolder(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	old := convertedIn(t, s, l, "old.mkv", false)
	moved := t.TempDir()
	l, err := s.UpdateLibrary(l.ID, nil, &moved)
	if err != nil {
		t.Fatal(err)
	}
	inside := convertedIn(t, s, l, "inside.mkv", false)
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(6)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureRevision(l.FlowID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LibraryAgain(l.ID, 7); err != nil || got.Earlier != 1 || got.Possible != 1 {
		t.Fatalf("earlier %+v %v, want only the file in the current folder", got, err)
	}
	if queued, _, err := s.ConvertLibraryAgain(l.ID, 7); err != nil || queued != 1 {
		t.Fatalf("bulk queued %d %v", queued, err)
	}
	if j, _ := s.Get(inside); j.Status != Queued {
		t.Errorf("the file in the current folder is %s, not queued", j.Status)
	}
	if j, _ := s.Get(old); j.Status != Done {
		t.Errorf("the file under the old folder was queued: %s", j.Status)
	}
}

func TestLibraryAgainNoSuchLibrary(t *testing.T) {
	s := open(t)
	if _, err := s.LibraryAgain(999, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func isAgainError(err error, code string) bool {
	var ae AgainError
	return errors.As(err, &ae) && ae.Code == code
}

// A job queued again after a workflow change (a retry, or a run its worker lost) runs with the current workflow, so it
// is stamped with the current revision and is not earlier when it finishes.
func TestRequeuedJobIsStampedWithCurrentWorkflow(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(4), true)
	failed, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "failed.mkv")})
	lost, _ := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "lost.mkv")})
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(failed, "boom", "failed under the old workflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetFlowTemplate(l.FlowID, rules.SliderTemplate(6)); err != nil {
		t.Fatal(err)
	}
	cur, err := s.EnsureRevision(l.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Retry(failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequeueRunning(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{failed, lost} {
		var rev int64
		if err := s.db.QueryRow(`SELECT workflow_rev FROM jobs WHERE id=?`, id).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		if rev != cur.ID {
			t.Errorf("job %d requeued with revision %d, want the current %d", id, rev, cur.ID)
		}
	}
}
