// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Migration is one schema step. The module that owns the tables registers it, so the store stays
// unaware of what each module stores. Versions are unique across all modules and run in order.
type Migration struct {
	Version int
	Name    string
	Up      func(tx *sql.Tx) error
}

var (
	registryMu sync.Mutex
	registry   []Migration
)

// Register adds migrations to the program's set; a module calls it from init. A version used twice
// is a programming mistake and panics.
func Register(m ...Migration) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, x := range m {
		if x.Version <= 0 {
			panic(fmt.Sprintf("store: migration %q has version %d; versions start at 1", x.Name, x.Version))
		}
		if x.Up == nil {
			panic(fmt.Sprintf("store: migration %d (%s) has no Up", x.Version, x.Name))
		}
		for _, have := range registry {
			if have.Version == x.Version {
				panic(fmt.Sprintf("store: migrations %q and %q both have version %d", have.Name, x.Name, x.Version))
			}
		}
		registry = append(registry, x)
	}
}

// registered is the program's migrations, in version order.
func registered() []Migration {
	registryMu.Lock()
	defer registryMu.Unlock()
	ms := append([]Migration(nil), registry...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms
}

// newestVersion is the highest migration version, or 0 when there are none.
func newestVersion(ms []Migration) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].Version
}

// Migrate brings the database to the newest registered version. An existing database is backed up
// first with VACUUM INTO. Every pending step runs in one transaction with the new schema version;
// a failure rolls everything back, and the error names the backup (L-SAFE-18).
func (db *DB) Migrate() error {
	var current int
	if err := db.write.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	newest := newestVersion(db.migrations)
	if current >= newest {
		return nil
	}
	var pending []Migration
	for _, m := range db.migrations {
		if m.Version > current {
			pending = append(pending, m)
		}
	}

	backup := ""
	if current > 0 { // an existing database: keep a copy before touching it
		backup = filepath.Join(db.dir, fmt.Sprintf("syp.db.bak-v%d", current))
		if err := db.backupTo(backup); err != nil {
			return fmt.Errorf("back up the database to %s before upgrading; nothing was upgraded: %w", backup, err)
		}
	}

	tx, err := db.write.Begin()
	if err != nil {
		return fmt.Errorf("start the upgrade: %w", err)
	}
	for _, m := range pending {
		if err := m.Up(tx); err != nil {
			tx.Rollback()
			return upgradeError(m, err, backup)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", newest)); err != nil {
		tx.Rollback()
		return upgradeError(pending[len(pending)-1], err, backup)
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback() // a commit that fails leaves the transaction open; take it back
		where := "; nothing was changed (a new database has no backup yet)"
		if backup != "" {
			where = "; nothing was changed, and the backup is " + backup
		}
		last := pending[len(pending)-1]
		return fmt.Errorf("save the upgraded schema (last step %d, %s): %v%s", last.Version, last.Name, err, where)
	}

	return pruneBackups(db.dir, 3)
}

// upgradeError names the step that failed and the backup the person can go back to.
func upgradeError(m Migration, err error, backup string) error {
	where := "; nothing was upgraded (a new database has no backup yet)"
	if backup != "" {
		where = "; nothing was upgraded, and the backup is " + backup
	}
	return fmt.Errorf("upgrade the schema at migration %d (%s) failed: %v%s", m.Version, m.Name, err, where)
}

// backupTo copies the database to path with VACUUM INTO. It writes a temp name beside the real one
// and renames it into place: an earlier backup of the same version (left by an upgrade that failed
// and rolled back) is replaced only once the new copy is complete, so a retry still has a backup.
// A failed attempt removes only the temp file.
func (db *DB) backupTo(path string) error {
	tmp := path + ".tmp"
	removeRegularFile(tmp) // a stale temp from an earlier failure is not a backup
	if _, err := db.write.Exec("VACUUM INTO ?", tmp); err != nil {
		removeRegularFile(tmp) // VACUUM INTO can leave a partial file
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		removeRegularFile(tmp)
		return err
	}
	return nil
}

// removeRegularFile removes path when it is a regular file; a folder in the way is not ours to remove.
func removeRegularFile(path string) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		os.Remove(path)
	}
}

// pruneBackups keeps the newest keep backups and deletes the rest. The versions come from the names
// and are compared as numbers, so v10 is newer than v9 (L-SAFE-18: keep the newest 3).
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list the data folder for old backups: %w", err)
	}
	var versions []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if v, ok := backupVersion(e.Name()); ok {
			versions = append(versions, v)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(versions)))
	var errs []error
	for i := keep; i < len(versions); i++ {
		if err := os.Remove(filepath.Join(dir, backupName(versions[i]))); err != nil {
			errs = append(errs, fmt.Errorf("remove the old backup of schema %d: %w", versions[i], err))
		}
	}
	return errors.Join(errs...)
}

// backupName is a backup's file name for a schema version.
func backupName(v int) string { return fmt.Sprintf("syp.db.bak-v%d", v) }

// backupVersion reads the schema version out of a backup's name, syp.db.bak-v<N>.
func backupVersion(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, "syp.db.bak-v")
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return v, true
}
