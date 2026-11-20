// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The user's "add a library, assign its workflow later": a library saved with no workflow has flow_id
// NULL (read as 0), is not started, and cannot be started until one is assigned.
func TestLibraryWithoutWorkflowCannotBeStarted(t *testing.T) {
	s := open(t)
	l, err := s.AddLibraryWithoutFlow("Later", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if l.FlowID != 0 || l.Started {
		t.Fatalf("%+v: want no flow (0) and not started", l)
	}
	// a stored 0 must be an absence, not a flow: everything that looks a flow up by this id refuses it
	if _, err := s.GetFlow(l.FlowID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetFlow(0): %v, want not found", err)
	}
	if _, err := s.TemplateFor(Job{LibraryID: l.ID}); err == nil {
		t.Error("a job of it has no setting to compile")
	}
	if err := s.StartLibrary(l.ID); !errors.Is(err, ErrNoWorkflow) {
		t.Errorf("start: %v, want ErrNoWorkflow", err)
	}
	if l, _ := s.GetLibrary(l.ID); l.Started {
		t.Error("the refused start must not have started it")
	}
	// assigning a workflow does not start it: the person starts it after
	flow, err := s.AddFlow("Films", rules.SliderTemplate(3))
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.SetLibraryFlow(l.ID, flow.ID); err != nil || l.Started || l.FlowID != flow.ID {
		t.Fatalf("assign: %+v %v, want the flow and still not started", l, err)
	}
	if _, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")}); !errors.Is(err, ErrLibraryGone) {
		t.Errorf("assign must queue nothing: %v", err)
	}
	if err := s.StartLibrary(l.ID); err != nil {
		t.Fatalf("start after assigning: %v", err)
	}
	if _, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")}); err != nil {
		t.Errorf("started with a workflow: %v", err)
	}
}

// Nothing is ever queued for a library with no workflow, whatever asked: the insert itself refuses, so a
// started flag written by hand (or a later start path that forgets the check) cannot get a job in.
func TestNothingIsQueuedForALibraryWithoutWorkflow(t *testing.T) {
	s := open(t)
	l, err := s.AddLibraryWithoutFlow("Later", t.TempDir(), true) // started as only an old or edited row could be
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv")}); !errors.Is(err, ErrLibraryGone) {
		t.Errorf("queued: %v, want ErrLibraryGone", err)
	}
	if _, err := s.EnqueueFailed(Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, "b.mkv")}, "empty_file", "x"); !errors.Is(err, ErrLibraryGone) {
		t.Errorf("a failed job: %v, want ErrLibraryGone", err)
	}
	list, err := s.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("jobs %+v, want none", list)
	}
}

// oldSchema56 opens a test database at version 55 (the shape before migration 56) with one flow and the
// named libraries, whose ids are 1..n in order.
func oldSchema56(t *testing.T, names ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "syp.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	// the runner's own temporary row, so steps that ask "upgraded from where" run as they would on a v55
	if _, err := db.Exec(`CREATE TEMP TABLE migrate_from (version INTEGER NOT NULL); INSERT INTO temp.migrate_from(version) VALUES (55)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:55] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`PRAGMA user_version=55`)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO flows(name,template,created,changed) VALUES('Films','{}',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, err := db.Exec(`INSERT INTO libraries(name,path,flow_id,started,created) VALUES(?,?,1,0,?)`, n, "/lib-"+n, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// Migration 56 rebuilds libraries, and its DROP would take the id sequence with it: a library removed
// before the upgrade must not have its id handed to the next one, which would then show the removed
// library's kept job history (dev-review on 14a26a33).
func TestMigration56KeepsTheIdSequence(t *testing.T) {
	for _, c := range []struct {
		name   string
		remove string // what was removed before the upgrade
		want   int64
	}{
		{"the highest library removed", `DELETE FROM libraries WHERE id=3`, 4},
		{"every library removed", `DELETE FROM libraries`, 4}, // ids 1..3 existed: the next is 4, not 1
	} {
		t.Run(c.name, func(t *testing.T) {
			path := oldSchema56(t, "One", "Two", "Three") // ids 1..3, the sequence at 3
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(c.remove); err != nil {
				t.Fatal(err)
			}
			db.Close()
			s, err := Open(path) // migrates 56 and up
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			l, err := s.AddLibraryWithoutFlow("Next", t.TempDir(), false)
			if err != nil {
				t.Fatal(err)
			}
			if l.ID != c.want {
				t.Errorf("a new library after the upgrade got id %d, want %d: a removed library's id must not come back", l.ID, c.want)
			}
		})
	}
}

// Migration 56 makes flow_id nullable so a library can be saved with no workflow, and keeps every row as
// it was (the table is made again: SQLite cannot drop NOT NULL in place).
func TestMigration56KeepsEveryLibrary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	// the runner's own temporary row, so steps that ask "upgraded from where" run as they would on a v55
	if _, err := db.Exec(`CREATE TEMP TABLE migrate_from (version INTEGER NOT NULL); INSERT INTO temp.migrate_from(version) VALUES (55)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:55] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`PRAGMA user_version=55`)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO flows(name,template,created,changed) VALUES('Films','{}',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for i, started := range []int{0, 1} {
		if _, err := db.Exec(`INSERT INTO libraries(name,path,flow_id,started,created,last_scan,last_scan_videos,priority)
VALUES(?,?,1,?,?,'2026-11-19T00:00:00Z',?,-3)`, "L"+string(rune('A'+i)), "/lib"+string(rune('A'+i)), started, now, i+4); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(path) // migrates 56 and up
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs, err := s.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 {
		t.Fatalf("%+v, want both rows", libs)
	}
	for i, l := range libs {
		if l.ID != int64(i+1) || l.Name != "L"+string(rune('A'+i)) || l.Path != "/lib"+string(rune('A'+i)) ||
			l.FlowID != 1 || l.Started != (i == 1) || l.LastScan != "2026-11-19T00:00:00Z" ||
			l.LastScanVideos != i+4 || l.Priority != -3 {
			t.Errorf("library %d: %+v", i, l)
		}
	}
	// the new shape: an id made after the rebuild does not reuse a removed one, and no flow may be stored
	gone, _ := s.AddLibraryWithoutFlow("Gone", t.TempDir(), false)
	s.DeleteLibrary(gone.ID)
	next, err := s.AddLibraryWithoutFlow("Next", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID <= gone.ID {
		t.Errorf("new id %d after removed %d: ids must not be reused (AUTOINCREMENT)", next.ID, gone.ID)
	}
}
