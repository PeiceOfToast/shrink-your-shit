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
	"runtime"
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
	} else if fi.Mode().Perm() != 0o600 && runtime.GOOS != "windows" { // Windows has no Unix mode bits
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

// The same refusal for a database in WAL mode, which is what this program makes: the main file's
// bytes are unchanged. A -shm or an empty -wal is SQLite's index, not data; a non-empty -wal would
// be the newer program's data and may not remain.
func TestANewerWalDatabaseIsRefusedBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "syp.db")
	newest := newestVersion(registered())

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := raw.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode is %q, want wal", mode)
	}
	for _, stmt := range []string{
		`CREATE TABLE t(id INTEGER)`,
		`INSERT INTO t(id) VALUES (1)`,
		fmt.Sprintf("PRAGMA user_version = %d", newest+1),
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open(dir); err == nil {
		t.Fatal("Open of a WAL database from a newer program succeeded")
	} else if !strings.Contains(err.Error(), "from a newer syp") {
		t.Errorf("the refusal is not the newer-syp sentence: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the main database file changed even though it was refused")
	}
	fi, walErr := os.Stat(path + "-wal")
	if walErr == nil {
		t.Logf("after Open: -wal is %d bytes", fi.Size())
		if fi.Size() > 0 {
			t.Errorf("a non-empty -wal file (%d bytes) remains after Open", fi.Size())
		}
	} else {
		t.Logf("after Open: no -wal file")
	}
	if _, shmErr := os.Stat(path + "-shm"); shmErr == nil {
		t.Log("after Open: a -shm file remains (an index, not data)")
	}
}

// A data folder whose name holds a URI character keeps its database inside it. Unescaped, SQLite
// reads # as a fragment and ? as a query, and puts syp.db in the parent under a truncated name.
func TestFoldersWithUriCharactersKeepTheirDatabaseInside(t *testing.T) {
	for _, name := range []string{"Movies #2", "What?", "100%"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(name, `<>:"|?*`) {
				t.Skip("not a legal Windows folder name")
			}
			parent := t.TempDir()
			dir := filepath.Join(parent, name)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			db, err := Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := db.Migrate(); err != nil {
				db.Close()
				t.Fatalf("Migrate: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "syp.db")); err != nil {
				t.Errorf("no syp.db inside %s: %v", dir, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if len(names) != 1 || names[0] != name {
				t.Errorf("the parent holds %v, want only the folder %q", names, name)
			}
		})
	}
}

// The writer's settings live in its DSN, so every connection the pool hands out has them: WAL,
// busy_timeout 5000, foreign keys on, synchronous NORMAL.
func TestTheWriterConnectionHasItsSettings(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	var busy, foreignKeys, synchronous int
	for _, c := range []struct {
		pragma string
		dst    any
	}{
		{"PRAGMA journal_mode", &mode},
		{"PRAGMA busy_timeout", &busy},
		{"PRAGMA foreign_keys", &foreignKeys},
		{"PRAGMA synchronous", &synchronous},
	} {
		if err := db.write.QueryRow(c.pragma).Scan(c.dst); err != nil {
			t.Fatalf("%s: %v", c.pragma, err)
		}
	}
	if mode != "wal" || busy != 5000 || foreignKeys != 1 || synchronous != 1 {
		t.Errorf("writer settings: journal_mode %q, busy_timeout %d, foreign_keys %d, synchronous %d; want wal, 5000, 1, 1",
			mode, busy, foreignKeys, synchronous)
	}
}

// Migrate refuses a database that became newer after Open (an out-of-band writer), with the same
// sentence as Open, and leaves the version alone (L-SAFE-18).
func TestMigrateRefusesADatabaseThatBecameNewer(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	newest := newestVersion(registered())
	future := newest + 9

	raw, err := sql.Open("sqlite", dbURI(filepath.Join(dir, "syp.db"), ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	err = db.Migrate()
	if err == nil {
		t.Fatal("Migrate accepted a database from a newer program")
	}
	for _, want := range []string{"from a newer syp", fmt.Sprintf("schema %d", future)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := userVersion(t, db); got != future {
		t.Errorf("user_version is %d, want %d (left alone)", got, future)
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

// L-SAFE-18, a failed backup: nothing is upgraded, no partial file is left, and any backup an
// earlier upgrade wrote is exactly as it was.
func TestAFailedBackupLeavesNoUpgradeAndNoPartialFile(t *testing.T) {
	ms := []Migration{tableMigration(1, "first"), tableMigration(2, "second")}

	t.Run("the backup path is a folder", func(t *testing.T) {
		dir := t.TempDir()
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
		if _, statErr := os.Stat(backup + ".tmp"); statErr == nil {
			t.Error("a temp file was left beside the backup")
		}
	})

	t.Run("an earlier backup is left alone", func(t *testing.T) {
		dir := t.TempDir()
		openAt(t, dir, ms, 1)
		backup := filepath.Join(dir, "syp.db.bak-v1")
		earlier := []byte("the only backup a failed upgrade left")
		if err := os.WriteFile(backup, earlier, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(backup+".tmp", 0o700); err != nil { // makes VACUUM INTO fail
			t.Fatal(err)
		}

		db, err := open(dir, ms)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := db.Migrate(); err == nil {
			t.Fatal("Migrate succeeded even though the backup failed")
		}
		if got, readErr := os.ReadFile(backup); readErr != nil || !bytes.Equal(got, earlier) {
			t.Errorf("the earlier backup was changed or removed: %v, %q", readErr, got)
		}
		if got := userVersion(t, db); got != 1 {
			t.Errorf("user_version is %d, want 1", got)
		}
		if tableExists(t, db, "second") {
			t.Error("the upgrade ran even though the backup failed")
		}
	})
}

// A retry after a failed upgrade must still have its backup (the failure left syp.db.bak-v1 behind,
// and the retry backs up over it) and must go through.
func TestARetryAfterAFailedUpgradeKeepsABackupAndUpgrades(t *testing.T) {
	dir := t.TempDir()
	fails := true
	second := Migration{
		Version: 2,
		Name:    "make second",
		Up: func(tx *sql.Tx) error {
			if fails {
				fails = false
				return errors.New("the second step broke once")
			}
			_, err := tx.Exec("CREATE TABLE second(id INTEGER)")
			return err
		},
	}
	ms := []Migration{tableMigration(1, "first"), second}
	openAt(t, dir, ms, 1)

	db, err := open(dir, ms)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err == nil {
		t.Fatal("the first attempt succeeded even though the second step fails once")
	}
	backup := filepath.Join(dir, "syp.db.bak-v1")
	if _, statErr := os.Stat(backup); statErr != nil {
		t.Fatalf("the failed upgrade left no backup to retry from: %v", statErr)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if got := userVersion(t, db); got != 2 {
		t.Errorf("user_version is %d, want 2", got)
	}
	if !tableExists(t, db, "second") {
		t.Error("the retry did not make table second")
	}

	raw, err := sql.Open("sqlite", "file:"+backup)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("the backup is not a database: %v", err)
	}
	if v != 1 {
		t.Errorf("the backup is at schema %d, want 1 (the version before the upgrade)", v)
	}
}

// A temp file in the data folder is not a backup: pruning leaves it alone and keeps three backups.
func TestPruneIgnoresATempBackup(t *testing.T) {
	dir := t.TempDir()
	for v := 1; v <= 4; v++ {
		if err := os.WriteFile(filepath.Join(dir, backupName(v)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tmp := filepath.Join(dir, backupName(5)+".tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneBackups(dir, 3); err != nil {
		t.Fatal(err)
	}
	if got, want := backupVersions(t, dir), []int{4, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("backups left: %v, want %v", got, want)
	}
	if _, statErr := os.Stat(tmp); statErr != nil {
		t.Error("the temp file was treated as a backup and removed")
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
