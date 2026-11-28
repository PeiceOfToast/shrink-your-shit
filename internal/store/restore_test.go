// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

// The set-up tables are registered by the packages that own them, so these tests import them for their migrations.
var _ = flow.Open
var _ = library.NewCatalog
var _ = settings.Load
var _ = system.Open

var restoreAt = time.Date(2026, 11, 28, 7, 0, 0, 0, time.UTC)

func openRestoreStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// exec runs one write through the database's own transaction.
func exec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	if err := db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setSetting(t *testing.T, db *store.DB, key, value string) {
	t.Helper()
	exec(t, db, `INSERT INTO app_settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
}

func addLibrary(t *testing.T, db *store.DB, id int64, name, path string) {
	t.Helper()
	exec(t, db, `INSERT INTO libraries(id, name, path) VALUES(?, ?, ?)`, id, name, path)
}

func settingOf(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var v string
	if err := db.Read().QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func libraryNameOf(t *testing.T, db *store.DB, id int64) string {
	t.Helper()
	var n string
	if err := db.Read().QueryRow(`SELECT name FROM libraries WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A restore brings back the set-up as the backup had it, keeps a library the backup does not have and lists it, and
// deletes nothing.
func TestRestoreBringsBackTheSetUpAndKeepsWhatTheBackupLacks(t *testing.T) {
	db := openRestoreStore(t)
	setSetting(t, db, "general.serverName", "Den")
	addLibrary(t, db, 1, "Films", "/a")
	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	setSetting(t, db, "general.serverName", "Den 2")
	addLibrary(t, db, 2, "Live only", "/b")
	exec(t, db, `UPDATE libraries SET name = 'Renamed' WHERE id = 1`)
	rep, err := db.RestoreSetUp(context.Background(), filepath.Join(db.BackupFolder(), b.ID+".db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := settingOf(t, db, "general.serverName"); got != "Den" {
		t.Errorf("serverName = %q, want the backup's Den", got)
	}
	if got := libraryNameOf(t, db, 1); got != "Films" {
		t.Errorf("library 1 name = %q, want the backup's Films", got)
	}
	if !rep.Restored.Settings || rep.Restored.Libraries != 1 {
		t.Errorf("restored = %+v", rep.Restored)
	}
	if len(rep.KeptNotInBackup) != 1 || rep.KeptNotInBackup[0].ID != "2" || rep.KeptNotInBackup[0].Type != "library" {
		t.Errorf("kept = %+v, want library 2 listed", rep.KeptNotInBackup)
	}
	if got := libraryNameOf(t, db, 2); got != "Live only" {
		t.Errorf("the library the backup lacks was changed or deleted: %q", got)
	}
}

// The restore never reads or writes the auth, tokens or sessions tables, whatever the backup holds.
func TestRestoreNeverTouchesSecrets(t *testing.T) {
	db := openRestoreStore(t)
	exec(t, db, `INSERT INTO auth(id, password_hash, set_at) VALUES(1, 'OLD-HASH', 1)`)
	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `UPDATE auth SET password_hash = 'NEW-HASH' WHERE id = 1`)
	if _, err := db.RestoreSetUp(context.Background(), filepath.Join(db.BackupFolder(), b.ID+".db"), nil); err != nil {
		t.Fatal(err)
	}
	var h string
	if err := db.Read().QueryRow(`SELECT password_hash FROM auth WHERE id = 1`).Scan(&h); err != nil {
		t.Fatal(err)
	}
	if h != "NEW-HASH" {
		t.Errorf("the password hash is %q after a restore, want the live NEW-HASH", h)
	}
}

// A download is a copy with the secret rows removed and compacted, so the hash is not in its bytes.
func TestStripSecretsRemovesTheSecretRows(t *testing.T) {
	db := openRestoreStore(t)
	exec(t, db, `INSERT INTO auth(id, password_hash, set_at) VALUES(1, 'SECRET-HASH-VALUE', 1)`)
	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(db.BackupFolder(), b.ID+".db")
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := store.StripSecrets(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET-HASH-VALUE") {
		t.Error("the password hash is still in the download's bytes")
	}
	stripped, err := sql.Open("sqlite", "file:"+dst+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer stripped.Close()
	var n int
	if err := stripped.QueryRow(`SELECT count(*) FROM auth`).Scan(&n); err != nil || n != 0 {
		t.Errorf("auth rows in the download = %d, %v; want 0", n, err)
	}
}

// A failure midway rolls the whole restore back: the settings changed before the failing table are as they were.
func TestRestoreRollsBackWhenItFailsMidway(t *testing.T) {
	db := openRestoreStore(t)
	setSetting(t, db, "general.serverName", "Den")
	addLibrary(t, db, 1, "Films", "/a")
	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	setSetting(t, db, "general.serverName", "Live")
	exec(t, db, `UPDATE libraries SET name = 'Live name' WHERE id = 1`)
	boom := errors.New("disk went away")
	_, err = db.RestoreSetUp(context.Background(), filepath.Join(db.BackupFolder(), b.ID+".db"), func(table string) error {
		if table == "libraries" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure", err)
	}
	if got := settingOf(t, db, "general.serverName"); got != "Live" {
		t.Errorf("serverName = %q after a failed restore, want the live Live (rolled back)", got)
	}
	if got := libraryNameOf(t, db, 1); got != "Live name" {
		t.Errorf("library name = %q after a failed restore, want Live name", got)
	}
}

// A backup library whose folder is already used under another id fails the restore, and nothing is left half-restored.
func TestRestoreRefusesAFolderTakenByAnotherLibrary(t *testing.T) {
	db := openRestoreStore(t)
	addLibrary(t, db, 1, "Films", "/a")
	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `DELETE FROM libraries WHERE id = 1`)
	addLibrary(t, db, 9, "Other", "/a")
	setSetting(t, db, "general.serverName", "Live")
	_, err = db.RestoreSetUp(context.Background(), filepath.Join(db.BackupFolder(), b.ID+".db"), nil)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("err = %v, want the folder conflict", err)
	}
	if got := settingOf(t, db, "general.serverName"); got != "Live" {
		t.Errorf("serverName = %q after the conflict, want Live (rolled back)", got)
	}
}

// Validation refuses a file that is not a backup, a file without the set-up tables, and a newer schema.
func TestValidateBackupRefusesWhatCannotBeRestored(t *testing.T) {
	db := openRestoreStore(t)
	live, err := db.CurrentSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("not a database at all, just text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ValidateBackup(context.Background(), junk, live); !errors.Is(err, store.ErrNotABackup) {
		t.Errorf("junk = %v, want not a backup", err)
	}

	b, err := db.MakeBackup("manual", "1", restoreAt)
	if err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(t.TempDir(), "newer.db")
	if err := os.WriteFile(newer, mustRead(t, filepath.Join(db.BackupFolder(), b.ID+".db")), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if _, _, err := store.ValidateBackup(context.Background(), newer, live); !errors.Is(err, store.ErrBackupTooNew) {
		t.Errorf("newer = %v, want backup too new", err)
	}
	if _, _, err := store.ValidateBackup(context.Background(), filepath.Join(db.BackupFolder(), b.ID+".db"), live); err != nil {
		t.Errorf("a good backup = %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
