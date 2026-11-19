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
		r.Message != "Update to 0.2.0 failed; rolled back to 0.1.0; changes made since the update were undone (kept in syp.db.rolled-back)" {
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
