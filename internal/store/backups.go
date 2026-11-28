// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Backups are copies of the whole database, kept in <data>/backups: <id>.db (the upgrade's own VACUUM INTO writer, see
// backupTo) and <id>.json, with the reason, the app version and the schema version. The id is the creation time in
// nanoseconds, so the newest backup has the largest id.

const backupsFolder = "backups"

// Backup is one kept copy of the database.
type Backup struct {
	ID            string    `json:"id"`
	Reason        string    `json:"reason"`
	AppVersion    string    `json:"appVersion"`
	SchemaVersion int       `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	SizeBytes     int64     `json:"sizeBytes"`
}

// ErrBackupNotFound is a backup that is not kept, or an id that cannot name one.
var ErrBackupNotFound = errors.New("no such backup")

// BackupFolder is where the backups are kept.
func (db *DB) BackupFolder() string { return filepath.Join(db.dir, backupsFolder) }

// MakeBackup copies the database now and keeps it with its reason and versions.
func (db *DB) MakeBackup(reason, appVersion string, now time.Time) (Backup, error) {
	dir := db.BackupFolder()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Backup{}, fmt.Errorf("make the backups folder: %w", err)
	}
	var schema int
	if err := db.Read().QueryRow(`PRAGMA user_version`).Scan(&schema); err != nil {
		return Backup{}, fmt.Errorf("read the schema version: %w", err)
	}
	id := strconv.FormatInt(now.UnixNano(), 10)
	path := filepath.Join(dir, id+".db")
	if err := db.backupTo(path); err != nil {
		return Backup{}, fmt.Errorf("copy the database: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Backup{}, err
	}
	b := Backup{ID: id, Reason: reason, AppVersion: appVersion, SchemaVersion: schema, CreatedAt: now.UTC(), SizeBytes: fi.Size()}
	raw, err := json.Marshal(b)
	if err != nil {
		return Backup{}, err
	}
	if err := writeFileAtomic(filepath.Join(dir, id+".json"), raw); err != nil {
		removeRegularFile(path)
		return Backup{}, fmt.Errorf("write the backup's details: %w", err)
	}
	return b, nil
}

// ListBackups answers the kept backups, newest first. A details file that does not read is skipped.
func (db *DB) ListBackups() ([]Backup, error) {
	entries, err := os.ReadDir(db.BackupFolder())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the backups: %w", err)
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(db.BackupFolder(), e.Name()))
		if err != nil {
			continue
		}
		var b Backup
		if err := json.Unmarshal(raw, &b); err != nil || b.ID+".json" != e.Name() {
			continue
		}
		if _, err := os.Stat(filepath.Join(db.BackupFolder(), b.ID+".db")); err != nil {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return backupIDLess(out[j].ID, out[i].ID) })
	return out, nil
}

// BackupPath is the file of a kept backup. An id that is not a backup's id is not found.
func (db *DB) BackupPath(id string) (string, error) {
	if !validBackupID(id) {
		return "", ErrBackupNotFound
	}
	path := filepath.Join(db.BackupFolder(), id+".db")
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", ErrBackupNotFound
	}
	return path, nil
}

// DeleteBackup removes a kept backup's two files.
func (db *DB) DeleteBackup(id string) error {
	path, err := db.BackupPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove the backup: %w", err)
	}
	removeRegularFile(filepath.Join(db.BackupFolder(), id+".json"))
	return nil
}

// PruneBackups keeps the newest keep backups and removes the rest. A backup named in protect is never removed, even
// past the count: the safety copy an apply made stays for the person who asked for the apply.
func (db *DB) PruneBackups(keep int, protect string) error {
	list, err := db.ListBackups()
	if err != nil {
		return err
	}
	var errs []error
	for i, b := range list {
		if i < keep || b.ID == protect {
			continue
		}
		if err := db.DeleteBackup(b.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// validBackupID is the id the store makes: digits, no sign, no leading zero. It keeps a crafted id out of a path.
func validBackupID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

// backupIDLess compares two backup ids as the numbers they are.
func backupIDLess(a, b string) bool {
	na, _ := strconv.ParseInt(a, 10, 64)
	nb, _ := strconv.ParseInt(b, 10, 64)
	return na < nb
}

// writeFileAtomic writes beside the path and renames it into place, so a reader sees the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
