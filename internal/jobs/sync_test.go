// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
)

// R1: the swap journal is only as safe as its commits. SQLite must sync each one (synchronous=FULL, 2),
// so a swap row RecoverSwaps trusts after a power cut was really on disk before the first rename.
func TestCommitsAreSynced(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("synchronous = %d (%v), want 2 (FULL)", n, err)
	}
}
