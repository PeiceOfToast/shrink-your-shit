// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readBackup decodes a backup file's whole content (the header and every table).
func readBackup(t *testing.T, path string) (hdr backupHeader, tables map[string][]map[string]any) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var whole struct {
		backupHeader
		Tables map[string][]map[string]any `json:"tables"`
	}
	if err := json.NewDecoder(gz).Decode(&whole); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return whole.backupHeader, whole.Tables
}

func TestBackupHoldsEveryTableAndItsRows(t *testing.T) {
	s, err := OpenAs(filepath.Join(t.TempDir(), "syp.db"), "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO app_meta(key, value) VALUES ('probe', 'hello')`); err != nil {
		t.Fatal(err)
	}
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if !backupID.MatchString(b.ID) || b.Reason != ReasonManual || b.AppVersion != "0.9.0" || b.SizeBytes <= 0 {
		t.Fatalf("info %+v", b)
	}
	hdr, tables := readBackup(t, filepath.Join(s.backupDir(), b.ID+".json.gz"))
	if hdr.FormatVersion != backupFormat || hdr.SchemaVersion != latestVersion() || hdr.AppVersion != "0.9.0" {
		t.Errorf("header %+v", hdr)
	}
	found := false
	for _, row := range tables["app_meta"] {
		if row["key"] == "probe" && row["value"] == "hello" {
			found = true
		}
	}
	if !found {
		t.Errorf("app_meta row missing: %v", tables["app_meta"])
	}
	for _, name := range []string{"jobs", "libraries", "flows"} {
		if _, ok := tables[name]; !ok {
			t.Errorf("table %s missing", name)
		}
	}
	for name := range tables {
		if len(name) >= 7 && name[:7] == "sqlite_" {
			t.Errorf("internal table %s in the backup", name)
		}
	}
	list, err := s.ListBackups()
	if err != nil || len(list) != 1 || list[0].ID != b.ID || list[0].Name == "" {
		t.Fatalf("list %+v %v", list, err)
	}
}

func TestAutomaticBackupsKeepTheNewestAndManualOnesStay(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	s.now = func() time.Time { at = at.Add(time.Minute); return at } // distinct times, so "newest" is clear
	if err := s.SetMeta("settings", `{"backupKeep":2}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteBackup(ReasonManual); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.WriteBackup(ReasonBeforeUpdate); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	manual, auto := 0, 0
	for _, b := range list {
		if b.Reason == ReasonManual {
			manual++
		} else {
			auto++
		}
	}
	if manual != 1 || auto != 2 {
		t.Fatalf("kept %d manual and %d automatic, want 1 and 2: %+v", manual, auto, list)
	}
	if list[0].Reason != ReasonBeforeUpdate || list[0].CreatedAt <= list[1].CreatedAt {
		t.Errorf("not newest first: %+v", list)
	}
}

func TestDeleteAndUnknownBackups(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, info, err := s.BackupFile(b.ID); err != nil || info.ID != b.ID {
		t.Fatalf("file %+v %v", info, err)
	}
	if err := s.DeleteBackup(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackup(b.ID); !errors.Is(err, ErrNoBackup) {
		t.Errorf("second delete: %v", err)
	}
	for _, id := range []string{"../syp", "x", "20261119-080000-abcd-manual/../../x"} {
		if _, _, err := s.BackupFile(id); !errors.Is(err, ErrNoBackup) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

// Opening a database from before the migrations makes a backup of its tables, before the upgrade.
func TestMigrationWritesAFileBackup(t *testing.T) {
	path := legacyDB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.ListBackups()
	if err != nil || len(list) != 1 || list[0].Reason != ReasonBeforeMigration {
		t.Fatalf("list %+v %v", list, err)
	}
}

// Backups made in the same minute get different names (ui-shop, #357 real-server test): the seconds are in it.
func TestBackupNamesTellSameMinuteBackupsApart(t *testing.T) {
	a := backupName(time.Date(2026, 11, 19, 11, 16, 5, 0, time.UTC))
	b := backupName(time.Date(2026, 11, 19, 11, 16, 40, 0, time.UTC))
	if a == b {
		t.Fatalf("two backups in the same minute share the name %q", a)
	}
	if want := "shrink-your-shit-backup-2026-11-19-111605.json.gz"; a != want {
		t.Errorf("name %q, want %q", a, want)
	}
}
