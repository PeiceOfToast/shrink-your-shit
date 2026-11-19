// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// setClock makes the store's events and file decisions happen at at, until the test ends.
func setClock(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	s.now = func() time.Time { return at }
	t.Cleanup(func() { s.now = time.Now })
}

// A scan's skip has no job, so it counts on the day its decision was written, not on no day at all.
func TestScanSkipCountsOnTheDayItWasDecided(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	day := noonDaysAgo(2)
	setClock(t, s, day)
	hevc := filepath.Join(l.Path, "hevc.mkv")
	if err := s.PutFile(File{Path: hevc, Size: 1, ModTime: 1, Decision: "skip: HEVC is already an efficient format", Reason: "hevc"}); err != nil {
		t.Fatal(err)
	}

	days := dayCounts(t, s)
	if got := days[day.Format("2006-01-02")].Skipped; got != 1 {
		t.Errorf("Days[%s].Skipped %d, want 1 (the scan skip on the day it was decided)", day.Format("2006-01-02"), got)
	}
	if _, skipped := sumDaysSkipped(days); skipped != 1 {
		t.Errorf("skipped over the window %d, want 1", skipped)
	}
}

// A file has one latest end: a scan skip decided after its conversion counts once, on the skip's day, and the
// conversion's day loses it. A conversion after the skip wins instead.
func TestScanSkipAndJobEndCountOnceOnTheLaterDay(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	converted := noonDaysAgo(3)
	skipped := noonDaysAgo(1)
	p := filepath.Join(l.Path, "again.mkv")

	setClock(t, s, converted)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: p, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(id, p, 400); err != nil {
		t.Fatal(err)
	}
	setClock(t, s, skipped)
	if err := s.PutFile(File{Path: p, Size: 400, ModTime: 2, Decision: "skip: already efficient", Reason: "efficient"}); err != nil {
		t.Fatal(err)
	}

	days := dayCounts(t, s)
	if got := days[converted.Format("2006-01-02")].Completed; got != 0 {
		t.Errorf("conversion day Completed %d, want 0: its latest end is the later skip", got)
	}
	if got := days[skipped.Format("2006-01-02")].Skipped; got != 1 {
		t.Errorf("skip day Skipped %d, want 1", got)
	}
}

// The migration adds decided_at: existing rows keep their values, and an existing skip is not given a time
// nothing recorded (NULL, so it counts on no day, as before).
func TestMigration48AddsDecidedAtAndKeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE files DROP COLUMN decided_at`, `PRAGMA user_version=47`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.db.Exec(`INSERT INTO files(path,size,mtime,codec,decision,reason) VALUES('/m/old.mkv',1,7,'hevc','skip: HEVC is already an efficient format','hevc')`)
	s.db.Exec(`INSERT INTO files(path,size,mtime,codec,decision,reason) VALUES('/m/q.mkv',2,8,'','queued: H.264 at 9000 kbps','')`)
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := version(t, s.db); v != latestVersion() {
		t.Fatalf("version %d after the upgrade, want %d", v, latestVersion())
	}
	var at *string
	var decision string
	var size, mtime int64
	if err := s.db.QueryRow(`SELECT decided_at, decision, size, mtime FROM files WHERE path='/m/old.mkv'`).Scan(&at, &decision, &size, &mtime); err != nil {
		t.Fatal(err)
	}
	if at != nil {
		t.Errorf("decided_at of an existing skip %q, want NULL (nothing recorded when it was decided)", *at)
	}
	if decision != "skip: HEVC is already an efficient format" || size != 1 || mtime != 7 {
		t.Errorf("existing skip row changed: decision %q, size %d, mtime %d", decision, size, mtime)
	}
	var q string
	s.db.QueryRow(`SELECT decision FROM files WHERE path='/m/q.mkv'`).Scan(&q)
	if q != "queued: H.264 at 9000 kbps" {
		t.Errorf("existing queued row changed: %q", q)
	}
}

// rewindDecidedAt takes away migration 48's column, for tests that set user_version below it (it runs again on the next open).
func rewindDecidedAt(s *Store) { s.db.Exec(`ALTER TABLE files DROP COLUMN decided_at`) }

// A file still changing (its first look, or a download in progress) is not a skip: it counts on no day, and a
// conversion it changes after is still that conversion's day.
func TestStillChangingCountsOnNoDay(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	day := noonDaysAgo(1)
	setClock(t, s, day)
	p := filepath.Join(l.Path, "downloading.mkv")
	if err := s.PutFile(File{Path: p, Size: 1, ModTime: 1, Decision: "skip: this file changed since we last looked (it may still be downloading). We'll look at it again on the next scan", Reason: "still_changing"}); err != nil {
		t.Fatal(err)
	}

	days := dayCounts(t, s)
	if _, skipped := sumDaysSkipped(days); skipped != 0 {
		t.Errorf("skipped over the window %d, want 0: a file still changing is not a skip", skipped)
	}
}

// A converted file that changes again keeps its conversion as its latest end, on the conversion's day.
func TestStillChangingAfterConversionKeepsTheConversionDay(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	converted := noonDaysAgo(3)
	changed := noonDaysAgo(1)
	p := filepath.Join(l.Path, "again.mkv")

	setClock(t, s, converted)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: p, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(id, p, 400); err != nil {
		t.Fatal(err)
	}
	setClock(t, s, changed)
	if err := s.PutFile(File{Path: p, Size: 500, ModTime: 2, Decision: "skip: this file changed since we last looked (it may still be downloading). We'll look at it again on the next scan", Reason: "still_changing"}); err != nil {
		t.Fatal(err)
	}

	days := dayCounts(t, s)
	if got := days[converted.Format("2006-01-02")].Completed; got != 1 {
		t.Errorf("conversion day Completed %d, want 1: a still-changing look is not its latest end", got)
	}
	if got := days[changed.Format("2006-01-02")].Skipped; got != 0 {
		t.Errorf("changed day Skipped %d, want 0", got)
	}
}

// An unreadable file is a skip the Skipped tab shows, so it counts on the day it was decided. A restored file
// (its original put back) has a reason too, but it is not a skip on its restore day.
func TestUnreadableCountsOnItsDayAndRestoredDoesNot(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	s.SetPaused(false)
	day := noonDaysAgo(2)
	setClock(t, s, day)
	bad := filepath.Join(l.Path, "bad.mkv")
	if err := s.PutFile(File{Path: bad, Size: 1, ModTime: 1, Decision: "unreadable", Reason: "unreadable"}); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(l.Path, "back.mkv")
	if err := s.PutFile(File{Path: back, Size: 1, ModTime: 1, Decision: DecisionRestored, Reason: DecisionRestored}); err != nil {
		t.Fatal(err)
	}

	days := dayCounts(t, s)
	if got := days[day.Format("2006-01-02")].Skipped; got != 1 {
		t.Errorf("Days[%s].Skipped %d, want 1 (the unreadable file only)", day.Format("2006-01-02"), got)
	}
	if _, skipped := sumDaysSkipped(days); skipped != 1 {
		t.Errorf("skipped over the window %d, want 1", skipped)
	}
}
