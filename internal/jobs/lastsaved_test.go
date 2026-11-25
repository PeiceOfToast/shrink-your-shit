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
