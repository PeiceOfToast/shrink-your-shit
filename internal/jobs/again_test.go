// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// orch-lead 13:44Z: Convert again's migration is 34, after the jobs-by-path index (33), which may already
// be on a database: one at 33 gets again_from on its next start, with its jobs as they were.
func TestUpgradeFrom33AddsAgainFrom(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	dropLogSections(t, s)
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	for _, q := range append(rewind35, `ALTER TABLE jobs DROP COLUMN again_from`, `PRAGMA user_version=33`) {
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
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != latestVersion() || v < 34 {
		t.Errorf("version %d", v)
	}
	if s.AgainFrom(id) != 0 {
		t.Error("again_from")
	}
	if _, err := s.db.Exec(`UPDATE jobs SET again_from=1 WHERE id=?`, id); err != nil {
		t.Errorf("no again_from column: %v", err)
	}
	if j, err := s.Get(id); err != nil || j.Path != "/m/a.mkv" {
		t.Errorf("%+v %v", j, err)
	}
}

// #163 QA: an original purged before its due date (the setting was lowered) is "deleted on" the day it
// went; one purged so before this fix, with only its due date on record, says that date for what it is.
func TestOriginalGoneWords(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	s.Claim("w")
	row, _ := s.BeginSwap(Original{JobID: id, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.1.mkv"})
	due := time.Now().Add(7 * 24 * time.Hour)
	s.FinishSwap(row, due)
	s.Complete(id, "/m/a.mkv", 1)
	s.db.Exec(`UPDATE originals SET state=? WHERE id=?`, OriginalPurged, row) // an old purge: the due date stays
	c, _ := s.ConvertAgainCheck(id, 7)
	if c.Possible || c.DeletedAt != "" || c.Message != "Can't convert again: the original has been deleted. The date wasn't recorded." {
		t.Errorf("old: %+v", c)
	}
	s.SetSwapState(row, OriginalPurged) // a purge now records when
	c, _ = s.ConvertAgainCheck(id, 7)
	if c.DeletedAt == "" || !strings.Contains(c.Message, "the original was deleted on "+time.Now().Local().Format("2 January 2006")+".") {
		t.Errorf("now: %+v", c)
	}
}

// A finished job goes back to the queue through Convert again only: the generic moves keep refusing
// Done -> Queued as they do on the base, so a job that finishes between a list of running jobs and a
// Release of each (web/v2workers.go) is not queued and converted a second time (review-583, finding 1).
func TestOnlyConvertAgainRequeuesAFinishedJob(t *testing.T) {
	s := open(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in", "a.mkv")
	if err := os.MkdirAll(filepath.Dir(in), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Library: filepath.Dir(in), Path: in, Reason: "r", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	// a new file in another folder: no original record, so "convert again" runs from the file still at its name
	if err := s.Complete(id, filepath.Join(dir, "out", "a.mkv"), 40); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"Release", func() error { return s.Release(id) }},
		{"RetryLater", func() error { return s.RetryLater(id, time.Now(), "convert_failed", "x") }},
		{"FallBack", func() error { return s.FallBack(id, "branch", nil, "", "x") }},
	} {
		if err := c.call(); err == nil || !strings.Contains(err.Error(), "cannot go from done to queued") {
			t.Errorf("%s on a finished job: %v, want cannot go from done to queued", c.name, err)
		}
		if j, err := s.Get(id); err != nil || j.Status != Done {
			t.Fatalf("%s changed the finished job: status %s, %v", c.name, j.Status, err)
		}
	}
	if err := s.ConvertAgain(id); err != nil {
		t.Fatalf("Convert again must still requeue a finished job: %v", err)
	}
	if j, err := s.Get(id); err != nil || j.Status != Queued {
		t.Errorf("after Convert again: status %s, %v; want queued", j.Status, err)
	}
}

// keptUntil is the date the purge deletes the kept original on: kept at keptAt plus the setting now, so
// changing keep-originals days moves it with the purge (not the date recorded when it was kept).
func TestKeptUntilFollowsTheKeepDaysSetting(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	s.Claim("w")
	row, _ := s.BeginSwap(Original{JobID: id, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.1.mkv"})
	s.FinishSwap(row, time.Now().AddDate(0, 0, 7))
	s.Complete(id, "/m/a.mkv", 1)
	o, _ := s.OriginalByID(row)
	for _, days := range []int{7, 2} { // the person lowers the setting after the conversion
		c, _ := s.ConvertAgainCheck(id, days)
		due, ok := KeptUntil(o.KeptAt, days)
		if !ok || c.KeptUntil != due.UTC().Format(time.RFC3339) {
			t.Errorf("%d days: keptUntil %q, the purge's date %v", days, c.KeptUntil, due)
		}
	}
}
