// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
)

// orch-lead 13:44Z: Convert again's migration is 34, after the jobs-by-path index (33), which may already
// be on a database: one at 33 gets again_from on its next start, with its jobs as they were.
func TestUpgradeFrom33AddsAgainFrom(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	for _, q := range []string{`ALTER TABLE jobs DROP COLUMN again_from`, `PRAGMA user_version=33`} {
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
