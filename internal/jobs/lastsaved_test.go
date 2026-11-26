// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The headline's lastDoneAt: the newest converted file, not a failure or a skip after it.
func TestLastSavedAtIsTheNewestConversion(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }

	if got, err := s.LastSavedAt(); err != nil || got != "" {
		t.Fatalf("no jobs: %q (%v), want \"\"", got, err)
	}

	a := filepath.Join(l.Path, "a.mkv")
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(id, a, 400); err != nil { // 09:00
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T09:00:00Z" {
		t.Errorf("after one conversion: %q, want 2026-11-20T09:00:00Z", got)
	}

	at = at.Add(time.Hour) // 10:00: a failure is not a save
	b := filepath.Join(l.Path, "b.mkv")
	bid, _ := s.Enqueue(Job{LibraryID: l.ID, Path: b, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Fail(bid, "x", "broken"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T09:00:00Z" {
		t.Errorf("a later failure moved it: %q, want 2026-11-20T09:00:00Z", got)
	}

	// A kept job counts only when its output is in place.
	at = at.Add(time.Hour) // 11:00
	k := filepath.Join(l.Path, "k.mkv")
	kid, _ := s.Enqueue(Job{LibraryID: l.ID, Path: k, SizeBefore: 1000})
	s.Claim("w")
	if _, err := s.db.Exec(`UPDATE jobs SET status='kept', output_path='', updated=? WHERE id=?`, s.stamp(), kid); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T09:00:00Z" {
		t.Errorf("kept with no output counted: %q", got)
	}
	if _, err := s.db.Exec(`UPDATE jobs SET output_path=? WHERE id=?`, k, kid); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T11:00:00Z" {
		t.Errorf("kept with output: %q, want 2026-11-20T11:00:00Z", got)
	}
}

// A kept job whose original was restored is not a save: the restore put the original back, so its time
// must not be the file's last saved time (the kept arm had no restored-original check).
func TestLastSavedAtSkipsARestoredKeptOriginal(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }

	a := filepath.Join(l.Path, "a.mkv")
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: a, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(id, a, 400); err != nil { // 09:00
		t.Fatal(err)
	}

	at = at.Add(2 * time.Hour) // 11:00: converted again and kept, then its original restored
	b := filepath.Join(l.Path, "b.mkv")
	bid, _ := s.Enqueue(Job{LibraryID: l.ID, Path: b, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(bid, b, 400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE jobs SET status='kept' WHERE id=?`, bid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO originals(job_id,library_id,path,trash,staged,original_bytes,original_mtime,original_hash,new_bytes,state,started)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, bid, l.ID, b, "", "", 1000, 0, "h", 400, OriginalRestored, s.stamp()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T09:00:00Z" {
		t.Errorf("a restored kept original moved it: %q, want 2026-11-20T09:00:00Z", got)
	}
}

// A path converted and then failed on a newer job is still one converted file, and its last saved time
// is still the conversion: LastSavedAt and the converted count must agree on it.
func TestDoneThenFailedOnTheSamePath(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at }

	p := filepath.Join(l.Path, "p.mkv")
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: p, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Complete(id, p, 400); err != nil { // 09:00
		t.Fatal(err)
	}

	at = at.Add(time.Hour) // 10:00: the same path fails on a newer job
	fid, _ := s.Enqueue(Job{LibraryID: l.ID, Path: p, SizeBefore: 1000})
	s.Claim("w")
	if err := s.Fail(fid, "x", "broken"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastSavedAt(); got != "2026-11-20T09:00:00Z" {
		t.Errorf("a later failure on the same path moved it: %q, want 2026-11-20T09:00:00Z", got)
	}
	if n, err := s.DoneCount(); err != nil || n != 1 {
		t.Errorf("converted files: %d (%v), want 1: the count and the time must agree", n, err)
	}
}
