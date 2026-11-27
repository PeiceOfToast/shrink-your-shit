// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// tableMigration is a schema step that makes one table, for tests that need several versions
// without the program's real migrations (a test registry never reaches Register).
func tableMigration(version int, table string) Migration {
	return Migration{
		Version: version,
		Name:    "make " + table,
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec("CREATE TABLE " + table + "(id INTEGER)")
			return err
		},
	}
}

// openAt opens dir with the test migrations up to version, migrates it and closes it, leaving a
// database as an older program would have left it.
func openAt(t *testing.T, dir string, ms []Migration, version int) {
	t.Helper()
	db, err := open(dir, ms[:version])
	if err != nil {
		t.Fatalf("open at version %d: %v", version, err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		t.Fatalf("look for table %s: %v", name, err)
	}
	return n > 0
}

func userVersion(t *testing.T, db *DB) int {
	t.Helper()
	var v int
	if err := db.Read().QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

func backupVersions(t *testing.T, dir string) []int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var versions []int
	for _, e := range entries {
		if v, ok := backupVersion(e.Name()); ok {
			versions = append(versions, v)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(versions)))
	return versions
}

// L-SAFE-17: a second program is refused, and the folder is free again once the first lets go.
func TestSecondOpenOfTheSameFolderIsRefused(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_, err = Open(dir)
	if err == nil || !strings.Contains(err.Error(), "Another syp is already using "+dir) {
		t.Fatalf("second Open gave %v, want \"Another syp is already using %s.\"", err, dir)
	}
	if fi, err := os.Stat(filepath.Join(dir, "syp.lock")); err != nil {
		t.Errorf("no lock file: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("the lock file is %v, want 0600", fi.Mode().Perm())
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// L-SAFE-18, before any write: a database from a newer program is refused, and its file is byte for
// byte what it was, with no -wal or -shm appearing beside it.
func TestANewerDatabaseIsRefusedBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "syp.db")
	newest := newestVersion(registered())
	future := newest + 1

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Open(dir)
	if err == nil {
		t.Fatal("Open of a database from a newer program succeeded")
	}
	for _, want := range []string{
		"from a newer syp",
		fmt.Sprintf("schema %d", future),
		fmt.Sprintf("knows %d", newest),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the database file changed even though it was refused")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Errorf("a %s file appeared beside the database", suffix)
		}
	}
}

// L-SAFE-18, one transaction: the third step of three fails, so the second step's table, the schema
// version and the backup are exactly what they were before.
func TestAFailingStepRollsEverythingBackAndNamesTheBackup(t *testing.T) {
	dir := t.TempDir()
	ms := []Migration{
		tableMigration(1, "first"),
		tableMigration(2, "second"),
		{Version: 3, Name: "third", Up: func(tx *sql.Tx) error {
			return errors.New("the third step broke")
		}},
	}
	openAt(t, dir, ms, 1)

	db, err := open(dir, ms)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backup := filepath.Join(dir, "syp.db.bak-v1")
	err = db.Migrate()
	if err == nil {
		t.Fatal("Migrate succeeded even though the third step fails")
	}
	if !strings.Contains(err.Error(), backup) {
		t.Errorf("the error does not name the backup %s: %v", backup, err)
	}
	if _, statErr := os.Stat(backup); statErr != nil {
		t.Errorf("the backup named in the error is not there: %v", statErr)
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version is %d, want 1", got)
	}
	if tableExists(t, db, "second") {
		t.Error("the second step's table survives a failed upgrade")
	}
	if !tableExists(t, db, "first") {
		t.Error("the first table is gone")
	}
}

// L-SAFE-18, a failed backup: nothing is upgraded and no partial file is left.
func TestAFailedBackupLeavesNoUpgradeAndNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	ms := []Migration{tableMigration(1, "first"), tableMigration(2, "second")}
	openAt(t, dir, ms, 1)

	backup := filepath.Join(dir, "syp.db.bak-v1")
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}

	db, err := open(dir, ms)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Migrate()
	if err == nil {
		t.Fatal("Migrate succeeded even though the backup failed")
	}
	if !strings.Contains(err.Error(), backup) {
		t.Errorf("the error does not name the backup %s: %v", backup, err)
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version is %d, want 1", got)
	}
	if tableExists(t, db, "second") {
		t.Error("the upgrade ran even though the backup failed")
	}
	if fi, statErr := os.Stat(backup); statErr != nil || !fi.IsDir() {
		t.Errorf("the folder at the backup path is gone: %v", statErr)
	}
}

// L-SAFE-18, keep the newest three: five upgrades leave three backups, and v10 outranks v9.
func TestOnlyTheNewestThreeBackupsAreKept(t *testing.T) {
	dir := t.TempDir()
	var ms []Migration
	for v := 1; v <= 11; v++ {
		ms = append(ms, tableMigration(v, fmt.Sprintf("t%d", v)))
	}
	openAt(t, dir, ms, 6) // a new database is not backed up

	for v := 7; v <= 11; v++ {
		db, err := open(dir, ms[:v])
		if err != nil {
			t.Fatalf("open for version %d: %v", v, err)
		}
		if err := db.Migrate(); err != nil {
			t.Fatalf("upgrade to %d: %v", v, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close after upgrade to %d: %v", v, err)
		}
	}

	if got, want := backupVersions(t, dir), []int{10, 9, 8}; !reflect.DeepEqual(got, want) {
		t.Errorf("backups left: %v, want %v", got, want)
	}
}

// A new database needs no backup, and its meta table has an install_id and an app_version.
func TestAFreshFolderMakesNoBackupAndSetsInstallID(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	if matches, _ := filepath.Glob(filepath.Join(dir, "syp.db.bak-v*")); len(matches) != 0 {
		t.Errorf("a new database was backed up: %v", matches)
	}
	var id string
	if err := db.Read().QueryRow(`SELECT value FROM meta WHERE key = 'install_id'`).Scan(&id); err != nil {
		t.Fatalf("read install_id: %v", err)
	}
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("install_id %q is not 16 hex characters", id)
	}
	var version string
	if err := db.Read().QueryRow(`SELECT value FROM meta WHERE key = 'app_version'`).Scan(&version); err != nil {
		t.Fatalf("read app_version: %v", err)
	}
	if version == "" {
		t.Error("app_version is empty")
	}
}
