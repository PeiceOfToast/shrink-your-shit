// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
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

// rewind35 takes away what migration 35 (remote workers) added.
var rewind35 = []string{`DROP TABLE worker_pairings`, `ALTER TABLE workers DROP COLUMN kind`, `ALTER TABLE workers DROP COLUMN token_hash`,
	`ALTER TABLE workers DROP COLUMN version`, `ALTER TABLE workers DROP COLUMN api_level`, `ALTER TABLE workers DROP COLUMN last_seen`,
	`ALTER TABLE workers DROP COLUMN capabilities`}
