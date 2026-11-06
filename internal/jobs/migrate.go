// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNewerDatabase is returned by Open when the database was written by a newer version of the
// program than this one. Opening it anyway could corrupt it, so the program must be upgraded.
var ErrNewerDatabase = errors.New("this database is from a newer shrink-your-shit; upgrade the program")

// A migration is one numbered schema step. The schema version lives in SQLite's
// "PRAGMA user_version". To change the schema, ADD a step at the end with the next number;
// never edit or reorder a step that has been released. Steps must be deterministic.
type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{1, "baseline tables", `
CREATE TABLE IF NOT EXISTS files (
  path TEXT PRIMARY KEY, size INTEGER, mtime INTEGER, codec TEXT, decision TEXT);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  library TEXT NOT NULL, path TEXT NOT NULL,
  status TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  worker TEXT NOT NULL DEFAULT '', duration REAL NOT NULL DEFAULT 0,
  size_before INTEGER NOT NULL DEFAULT 0, size_after INTEGER NOT NULL DEFAULT 0,
  output_path TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status);`},
	// "IF NOT EXISTS" above is what lets a database made before migrations existed (version 0,
	// tables already there) be adopted as version 1 without touching its data.

	{2, "app_meta table", `
CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO app_meta(key, value) VALUES ('schema_created', datetime('now'));`},
}

// keepBackups is how many pre-upgrade backups (<db>.bak-v<old version>) are kept.
const keepBackups = 3

func latestVersion() int { return migrations[len(migrations)-1].version }

func (s *Store) userVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// checkNotNewer refuses a database from a newer program, before anything is written to it.
func (s *Store) checkNotNewer() error {
	v, err := s.userVersion()
	if err != nil {
		return fmt.Errorf("read database version: %w", err)
	}
	if v > latestVersion() {
		return fmt.Errorf("%w (database schema version %d, this program understands up to %d)", ErrNewerDatabase, v, latestVersion())
	}
	return nil
}

// hasTables reports whether the database already holds data tables (an existing install).
func (s *Store) hasTables() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`).Scan(&n)
	return n > 0, err
}

// migrate applies every step newer than the database's version, all in one transaction: if any
// step fails nothing is changed and Open fails. Before upgrading an existing database it saves a
// copy next to it.
func (s *Store) migrate() error {
	cur, err := s.userVersion()
	if err != nil {
		return fmt.Errorf("read database version: %w", err)
	}
	var pending []migration
	for _, m := range migrations {
		if m.version > cur {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	existing, err := s.hasTables()
	if err != nil {
		return err
	}
	backup := ""
	if existing {
		if backup, err = s.backup(cur); err != nil {
			return fmt.Errorf("could not back up the database before upgrading it (%v); not upgrading", err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit
	for _, m := range pending {
		if _, err := tx.Exec(m.sql); err != nil {
			return failedMigration(m, err, backup)
		}
	}
	// PRAGMA takes no bound parameters; the number is ours. It is part of the same transaction.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", latestVersion())); err != nil {
		return failedMigration(pending[len(pending)-1], err, backup)
	}
	if err := tx.Commit(); err != nil {
		return failedMigration(pending[len(pending)-1], err, backup)
	}
	return nil
}

func failedMigration(m migration, err error, backup string) error {
	msg := fmt.Sprintf("database upgrade step %d (%s) failed: %v. The database was left as it was", m.version, m.name, err)
	if backup != "" {
		msg += "; a copy from before the upgrade is at " + backup
	}
	return errors.New(msg)
}

// backup saves a consistent copy of the database as <db>.bak-v<version> (replacing one from a
// previous attempt) and deletes all but the newest keepBackups of them.
func (s *Store) backup(version int) (string, error) {
	dest := fmt.Sprintf("%s.bak-v%d", s.path, version)
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	// VACUUM INTO writes a complete, consistent copy, including anything still in the WAL file.
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		os.Remove(dest) // a failed copy (disk full...) must not be left looking like a backup
		return "", err
	}
	s.pruneBackups()
	return dest, nil
}

func (s *Store) pruneBackups() {
	matches, _ := filepath.Glob(s.path + ".bak-v*")
	type file struct {
		path string
		mod  int64
	}
	var files []file
	for _, m := range matches {
		if strings.HasSuffix(m, "-wal") || strings.HasSuffix(m, "-shm") {
			continue
		}
		if st, err := os.Stat(m); err == nil {
			files = append(files, file{m, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	for i := keepBackups; i < len(files); i++ {
		os.Remove(files[i].path)
	}
}
