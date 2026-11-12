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
