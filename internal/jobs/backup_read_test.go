// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openBackupStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAs(filepath.Join(t.TempDir(), "syp.db"), "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// duringDump runs a manual backup and holds it open after its first table is read, then runs fn while it is held.
// fn must finish within a few seconds, or the store was waiting for the dump. It returns the finished backup.
// hangBound is only how long a step may take before the test calls it hung. Both waits are on events, so a slow
// runner just takes longer; nothing here depends on the dump finishing within it.
const hangBound = 30 * time.Second

func duringDump(t *testing.T, s *Store, fn func() error) BackupInfo {
	t.Helper()
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	dumpBetweenTables = func() {
		once.Do(func() {
			close(paused)
			<-resume
		})
	}
	t.Cleanup(func() { dumpBetweenTables = nil })
	type result struct {
		info BackupInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := s.WriteBackup(ReasonManual)
		done <- result{info, err}
	}()
	select {
	case <-paused:
	case r := <-done:
		t.Fatalf("the backup ended before it was held open: %v", r.err)
	case <-time.After(hangBound):
		t.Fatal("the backup never reached a table")
	}
	fnDone := make(chan error, 1)
	go func() { fnDone <- fn() }()
	select {
	case err := <-fnDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(hangBound):
		t.Error("the store waited for the backup dump to finish")
	}
	close(resume)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r.info
}

// While a dump runs, the store's own connection is free: a write completes before the dump ends.
func TestBackupDoesNotHoldTheStoreWhileItRuns(t *testing.T) {
	s := openBackupStore(t)
	duringDump(t, s, func() error {
		_, err := s.db.Exec(`INSERT INTO app_meta(key, value) VALUES ('during', 'dump')`)
		return err
	})
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM app_meta WHERE key = 'during'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the write made during the dump: %d rows, %v", n, err)
	}
}

// Rows written while a dump runs are either all in the backup or none are. The dump's read starts before the
// first table, so none are: the backup is the database as it was when the dump began, and it still previews.
func TestBackupSnapshotTakesAllOrNoneOfTheRowsWrittenDuringIt(t *testing.T) {
	s := openBackupStore(t)
	info := duringDump(t, s, func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for i := 0; i < 5; i++ {
			if _, err := tx.Exec(`INSERT INTO app_meta(key, value) VALUES (?, 'during')`, "snap-"+string(rune('a'+i))); err != nil {
				tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
	path := filepath.Join(s.backupDir(), info.ID+".json.gz")
	_, tables := readBackup(t, path)
	inBackup := 0
	for _, row := range tables["app_meta"] {
		if v, _ := row["value"].(string); v == "during" {
			inBackup++
		}
	}
	if inBackup != 0 && inBackup != 5 {
		t.Fatalf("the backup holds %d of the 5 rows written during the dump; want all or none", inBackup)
	}
	if inBackup != 0 {
		t.Errorf("the backup holds %d rows written after the dump began; want none", inBackup)
	}
	var live int
	if err := s.db.QueryRow(`SELECT count(*) FROM app_meta WHERE value = 'during'`).Scan(&live); err != nil || live != 5 {
		t.Fatalf("the store holds %d of the 5 rows: %v", live, err)
	}
	if _, err := s.PreviewBackup(path); err != nil {
		t.Fatalf("the backup does not preview: %v", err)
	}
}
