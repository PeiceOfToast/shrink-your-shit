// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R14: the replace step's file actions are kept in the order the worker took them, each with what it did, to
// which file, whether it worked and the system's words when it did not.
func TestJobMovesKeepTheirOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	want := []Move{
		{Action: "chmod", From: "/m/x", To: "/m/x", OK: true},
		{Action: "chown", From: "/m/x", To: "/m/x", OK: false, Error: "chown /m/x: permission denied"},
		{Action: "move", From: "/m/.a.123.syp-tmp/new.mkv", To: "/m/a.mkv", OK: true},
		{Action: "remove", From: "/m/.a.123.syp-tmp", OK: true},
	}
	for _, m := range want {
		if err := s.RecordJobMove(id, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.JobMoves(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("moves %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("move %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	// a job that recorded none: none, not an error
	if none, err := s.JobMoves(id + 1); err != nil || len(none) != 0 {
		t.Errorf("a job with no moves: %+v, %v", none, err)
	}
}

// A job removed from the database takes its file actions with it.
func TestRemoveJobLogsDeletesMoves(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gone, _ := s.Enqueue(Job{Library: "/m", Path: "/m/gone.mkv"})
	kept, _ := s.Enqueue(Job{Library: "/m", Path: "/m/kept.mkv"})
	s.RecordJobMove(gone, Move{Action: "chmod", OK: true})
	s.RecordJobMove(kept, Move{Action: "chmod", OK: true})
	if err := s.RemoveJobLogs([]int64{gone}); err != nil {
		t.Fatal(err)
	}
	if moves, _ := s.JobMoves(gone); len(moves) != 0 {
		t.Errorf("the removed job's moves stayed: %+v", moves)
	}
	if moves, _ := s.JobMoves(kept); len(moves) != 1 {
		t.Errorf("another job's moves went: %+v", moves)
	}
}

// Pruning an old job's log prunes its file actions too (they are part of the log, not the summary).
func TestPruneJobLogsDeletesMoves(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetPaused(false)
	l, _ := s.AddLibrary("m", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := s.Enqueue(Job{Library: "m", LibraryID: l.ID, Path: "/m/old.mkv"})
	s.Claim("t")
	s.RecordJobMove(id, Move{Action: "move", From: "/m/.old.1.syp-tmp/new.mkv", To: "/m/old.mkv", OK: true})
	s.Complete(id, "/m/old.mkv", 1)
	if _, err := s.PruneJobLogs(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if moves, _ := s.JobMoves(id); len(moves) != 0 {
		t.Errorf("the pruned job's moves stayed: %+v", moves)
	}
}
