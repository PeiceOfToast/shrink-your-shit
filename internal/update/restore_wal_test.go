// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Review #447 P2: a newer version stopped with commits still only in its WAL (not checkpointed). After a
// restore, the kept database opens and has them: "jobs done on the newer version are kept" holds.
func TestRestoredDatabaseKeepsUncheckpointedCommits(t *testing.T) {
	live := filepath.Join(t.TempDir(), "syp.db")
	conn, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE t(x)", "INSERT INTO t VALUES (42)"} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	// what the stopped program leaves: the main file and its WAL (copied while open, so nothing is checkpointed)
	dir := t.TempDir()
	db := filepath.Join(dir, "syp.db")
	for _, s := range []string{"", "-wal"} {
		b, err := os.ReadFile(live + s)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(db+s, b, 0o600)
	}
	backup := filepath.Join(dir, "syp.db.bak-v1")
	os.WriteFile(backup, nil, 0o600)
	kept, err := RestoreDB(backup, db)
	if err != nil {
		t.Fatal(err)
	}
	k, err := sql.Open("sqlite", kept)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	var x int
	if err := k.QueryRow("SELECT x FROM t").Scan(&x); err != nil || x != 42 {
		t.Errorf("the kept database lost its commit: x=%d, %v", x, err)
	}
}
