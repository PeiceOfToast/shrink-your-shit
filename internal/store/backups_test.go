// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func openBackupStore(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// A backup is a copy of the database with its reason, app version and schema version, and it lists newest first.
func TestMakeBackupKeepsTheCopyAndItsDetails(t *testing.T) {
	db := openBackupStore(t)
	at := time.Date(2026, 11, 28, 7, 0, 0, 0, time.UTC)
	b, err := db.MakeBackup("manual", "1.2.3", at)
	if err != nil {
		t.Fatal(err)
	}
	if b.Reason != "manual" || b.AppVersion != "1.2.3" || b.SchemaVersion == 0 || b.SizeBytes == 0 || !b.CreatedAt.Equal(at) {
		t.Errorf("details = %+v", b)
	}
	raw, err := os.ReadFile(db.BackupFolder() + "/" + b.ID + ".db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "SQLite format 3\x00") {
		t.Error("the copy is not a SQLite database")
	}
	list, err := db.ListBackups()
	if err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("list = %+v, %v", list, err)
	}
}

// Prune keeps the newest keep backups, and never removes the one it is told to protect, even past the count.
func TestPruneKeepsNewestAndProtected(t *testing.T) {
	db := openBackupStore(t)
	base := time.Date(2026, 11, 28, 7, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 4; i++ {
		b, err := db.MakeBackup("manual", "1", base.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	oldest := ids[0]
	if err := db.PruneBackups(2, oldest); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, b := range list {
		kept[b.ID] = true
	}
	if len(list) != 3 || !kept[ids[3]] || !kept[ids[2]] || !kept[oldest] || kept[ids[1]] {
		t.Errorf("after prune keep 2 (protecting the oldest) = %v, want the two newest and the protected one", kept)
	}
}

// Ids are numbers the store made. Anything else cannot name a path outside the backups folder.
func TestBackupIDsCannotNameAPath(t *testing.T) {
	db := openBackupStore(t)
	for _, id := range []string{"", "0", "-1", "../x", "1/../2", "01", "1.db", "abc"} {
		if _, err := db.BackupPath(id); !errors.Is(err, ErrBackupNotFound) {
			t.Errorf("BackupPath(%q) = %v, want not found", id, err)
		}
		if err := db.DeleteBackup(id); !errors.Is(err, ErrBackupNotFound) {
			t.Errorf("DeleteBackup(%q) = %v, want not found", id, err)
		}
	}
}

// Delete removes both files, and a second delete finds nothing.
func TestDeleteBackupRemovesBothFiles(t *testing.T) {
	db := openBackupStore(t)
	b, err := db.MakeBackup("manual", "1", time.Date(2026, 11, 28, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteBackup(b.ID); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".db", ".json"} {
		if _, err := os.Stat(db.BackupFolder() + "/" + b.ID + ext); !os.IsNotExist(err) {
			t.Errorf("%s still exists after delete: %v", ext, err)
		}
	}
	if err := db.DeleteBackup(b.ID); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("second delete = %v, want not found", err)
	}
}
