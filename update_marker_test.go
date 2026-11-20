// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// A start after a failed update: the rolled-back program finds a database the new version changed,
// puts the backup back, opens it, and records the failure.
func TestStartAfterARollBackRestoresTheDatabase(t *testing.T) {
	data := t.TempDir()
	db := filepath.Join(data, "syp.db")
	st, err := jobs.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := st.Backup()
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	raw, err := sql.Open("sqlite", db) // the new version moved the schema on
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if _, err := jobs.Open(db); !errors.Is(err, jobs.ErrNewerDatabase) {
		t.Fatalf("setup: open = %v, want ErrNewerDatabase", err)
	}

	update.WriteMarker(data, update.Marker{Kind: "update", From: "0.1.0", To: "0.2.0", Backup: backup})
	pending := update.ReadMarker(data)
	st, err = openStore(db, "", pending)
	if err != nil {
		t.Fatalf("open after the roll back: %v", err)
	}
	st.Close()
	if _, err := os.Stat(db + ".rolled-back"); err != nil {
		t.Errorf("the newer database was not kept: %v", err)
	}
	if m := update.ReadMarker(data); m == nil || !m.DataRestored { // saved, in case this start stops early
		t.Fatalf("marker after the restore: %+v", m)
	}
	r, err := update.Settle(data, pending, "0.1.0", time.Now())
	if err != nil || r.OK || !r.DataRestored ||
		r.Message != "Update to 0.2.0 failed; rolled back to 0.1.0; changes made since the update were undone: jobs done on the newer version are kept in syp.db.rolled-back" {
		t.Errorf("result %+v %v", r, err)
	}
	if update.ReadMarker(data) != nil {
		t.Error("the marker is still there")
	}
}

// A start of the new version: the database opens as it is and the update is recorded once.
func TestStartOfTheNewVersionRecordsSuccess(t *testing.T) {
	data := t.TempDir()
	db := filepath.Join(data, "syp.db")
	update.WriteMarker(data, update.Marker{Kind: "update", From: "0.1.0", To: "0.2.0", Backup: db + ".bak-v1"})
	pending := update.ReadMarker(data)
	st, err := openStore(db, "", pending)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := os.Stat(db + ".rolled-back"); !os.IsNotExist(err) {
		t.Errorf("a database was put back on a good update: %v", err)
	}
	if r, _ := update.Settle(data, pending, "0.2.0", time.Now()); !r.OK || r.DataRestored || r.Message != "Updated from 0.1.0 to 0.2.0" {
		t.Errorf("result %+v", r)
	}
	if update.ReadMarker(data) != nil || update.LastResult(data) == nil {
		t.Error("the marker should be gone and the result stored")
	}
}

// newerDatabase makes data/syp.db as a newer version leaves it, after this version saved its
// pre-upgrade copy (syp.db.bak-v<SchemaVersion>) when withBackup.
func newerDatabase(t *testing.T, withBackup bool) (data, db string) {
	t.Helper()
	data = t.TempDir()
	db = filepath.Join(data, "syp.db")
	st, err := jobs.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if withBackup {
		if _, err := st.Backup(); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	return data, db
}

// User item 9: the update was settled (or there was none: an older Docker image, a program put back by
// hand), then the older program starts on the newer database. It puts the pre-upgrade copy back, keeps
// the newer database and any one kept before, and says where the newer version's jobs are.
func TestOlderProgramWithoutAMarkerPutsTheCopyBack(t *testing.T) {
	data, db := newerDatabase(t, true)
	os.WriteFile(db+".rolled-back", []byte("from an earlier roll back"), 0o600)
	st, err := openStore(db, "0.1.0", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st.Close()
	if b, _ := os.ReadFile(db + ".rolled-back"); string(b) != "from an earlier roll back" {
		t.Errorf("the database kept before was replaced: %q", b)
	}
	if _, err := os.Stat(db + ".rolled-back-2"); err != nil {
		t.Errorf("the newer database was not kept: %v", err)
	}
	r := update.LastResult(data)
	if r == nil || r.OK || !r.RolledBack || !r.DataRestored || r.Message !=
		"A newer version had upgraded the database; this version put back the copy from before that upgrade; changes made since the update were undone: jobs done on the newer version are kept in syp.db.rolled-back-2" {
		t.Errorf("result %+v", r)
	}
}

// A marker that names no backup (or another one) still puts back the exact version's copy.
func TestMarkerWithoutABackupStillPutsTheCopyBack(t *testing.T) {
	data, db := newerDatabase(t, true)
	update.WriteMarker(data, update.Marker{Kind: "update", From: "0.1.0", To: "0.2.0"})
	pending := update.ReadMarker(data)
	st, err := openStore(db, "0.1.0", pending)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st.Close()
	if m := update.ReadMarker(data); m == nil || !m.DataRestored || m.KeptAs != "syp.db.rolled-back" {
		t.Fatalf("marker after the restore: %+v", m)
	}
}

// No copy for this version: the same clear error as before, and nothing is changed or moved.
func TestNoCopyForThisVersionStopsWithoutChanges(t *testing.T) {
	data, db := newerDatabase(t, false)
	os.WriteFile(db+".bak-v1", []byte("a copy for another version"), 0o600)
	if _, err := openStore(db, "0.1.0", nil); !errors.Is(err, jobs.ErrNewerDatabase) {
		t.Fatalf("open = %v, want ErrNewerDatabase", err)
	}
	if _, err := os.Stat(db + ".rolled-back"); !os.IsNotExist(err) {
		t.Errorf("a database was moved: %v", err)
	}
	if update.LastResult(data) != nil {
		t.Error("a result was written")
	}
	raw, _ := sql.Open("sqlite", db)
	defer raw.Close()
	var v int
	if raw.QueryRow("PRAGMA user_version").Scan(&v); v != 9999 {
		t.Errorf("the newer database changed: version %d", v)
	}
}
