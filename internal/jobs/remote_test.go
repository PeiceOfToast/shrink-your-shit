// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// orch-lead 13:44Z: remote workers are migration 35, after 33 and 34, which may already be on a
// database: one at 34 gets the remote worker columns and pairings on its next start, and pairs.
func TestUpgradeFrom34AddsRemoteWorkers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range append(rewind35, `PRAGMA user_version=34`) {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	if s, err = Open(p); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != latestVersion() || v < 35 {
		t.Errorf("version %d", v)
	}
	code, _, err := s.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w, tok, err := s.Pair(code, "Desktop", "1", 1, "", time.Now()); err != nil || tok == "" {
		t.Errorf("%+v %v", w, err)
	}
}

// rewind35 takes away what migrations 35 (remote workers) and 36 (workers on and off) added.
var rewind35 = []string{`ALTER TABLE workers DROP COLUMN enabled`, `DROP INDEX workers_token`, `DROP TABLE worker_pairings`, `ALTER TABLE workers DROP COLUMN kind`, `ALTER TABLE workers DROP COLUMN token_hash`,
	`ALTER TABLE workers DROP COLUMN version`, `ALTER TABLE workers DROP COLUMN api_level`, `ALTER TABLE workers DROP COLUMN last_seen`,
	`ALTER TABLE workers DROP COLUMN capabilities`}

// review 95756cc: every worker call finds its worker by its token's hash, by index.
func TestWorkerByTokenUsesItsIndex(t *testing.T) {
	s := open(t)
	var plan string
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM workers WHERE kind='remote' AND token_hash=?`, "x")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan += d + "; "
	}
	rows.Close()
	if !strings.Contains(plan, "workers_token") {
		t.Errorf("plan %s", plan)
	}
}

// User review 11-12 item 2 (orch-lead 14:22Z): each worker's own flag is its only on/off state; the
// switch for all sets every flag; today's pause carries over; a paired worker starts on.
func TestWorkersOnAndOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Paused() || s.WorkerEnabled(LocalWorker) { // a new install starts stopped (S2), carried to the local worker
		t.Fatalf("new install: paused %v, local on %v", s.Paused(), s.WorkerEnabled(LocalWorker))
	}
	code, _, _ := s.NewPairing(time.Now())
	rw, _, err := s.Pair(code, "Desktop", "1", 1, "", time.Now())
	if err != nil || !s.WorkerEnabled(rw.ID) {
		t.Fatalf("a paired worker starts on: %v %v", s.WorkerEnabled(rw.ID), err)
	}
	if s.Paused() {
		t.Error("one worker on: not paused")
	}
	s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	if _, err := s.Claim("builtin"); err != ErrNoJob {
		t.Errorf("the local worker is off, yet claimed: %v", err)
	}
	if j, err := s.Claim(rw.ID); err != nil || j.Worker != rw.ID {
		t.Errorf("the remote worker is on: %+v %v", j, err)
	}
	if err := s.SetPaused(true); err != nil || !s.Paused() || s.WorkerEnabled(rw.ID) || s.WorkerEnabled(LocalWorker) {
		t.Errorf("all off: %v", err)
	}
	if err := s.SetPaused(false); err != nil || s.Paused() || !s.WorkerEnabled(rw.ID) || !s.WorkerEnabled(LocalWorker) {
		t.Errorf("all on: %v", err)
	}
	s.SetWorkerEnabled(LocalWorker, false)
	if s.WorkerEnabled("builtin-2") || !s.WorkerEnabled(rw.ID) {
		t.Error("the pool follows the local worker's flag, a remote worker its own")
	}
	if err := s.SetWorkerEnabled("w-none", false); err != ErrNotFound {
		t.Errorf("no such worker: %v", err)
	}
	var events int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind IN ('processing.paused','processing.resumed')`).Scan(&events)
	if events != 3 { // resumed (paired), paused (all off), resumed (all on): not for the local one alone
		t.Errorf("pause events %d", events)
	}
	s.Close()
	// an install paused before this version: every worker off
	s, _ = Open(p)
	s.SetPaused(true)
	for _, q := range append([]string{`INSERT INTO app_meta(key, value) VALUES ('paused', '1')`, `UPDATE workers SET enabled=1`}, `ALTER TABLE workers DROP COLUMN enabled`, `PRAGMA user_version=35`) {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	s, _ = Open(p)
	defer s.Close()
	if !s.Paused() || s.WorkerEnabled(rw.ID) || s.WorkerEnabled(LocalWorker) {
		t.Error("the pause wasn't carried to every worker")
	}
}
