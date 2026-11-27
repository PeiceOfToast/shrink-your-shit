// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package store opens the database, locks the data folder and runs migrations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"

	_ "modernc.org/sqlite" // the database driver, registered as "sqlite"
)

// DB is an open data folder: its database, its lock, and the migrations it was opened with.
type DB struct {
	dir        string
	write      *sql.DB
	read       *sql.DB
	unlock     func() error
	migrations []Migration

	closeOnce sync.Once
	closeErr  error
}

// Open opens dir, the data folder. The folder must be local (a network share is refused, K15) and
// one program at a time may have it (L-SAFE-17). A database written by a newer program is refused
// before anything is written to it (L-SAFE-18).
func Open(dir string) (*DB, error) {
	return open(dir, registered())
}

// open is Open with a migration set, so tests can use their own.
func open(dir string, migrations []Migration) (*DB, error) {
	ms := append([]Migration(nil), migrations...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("make the data folder %s: %w", dir, err)
	}
	network, kind, err := fsx.IsNetwork(dir)
	if err != nil {
		return nil, err
	}
	if network {
		return nil, fmt.Errorf("The data folder %s is on a network share (%s). Use a local folder.", dir, kind)
	}

	unlock, err := fsx.Lock(filepath.Join(dir, "syp.lock"))
	if err != nil {
		if errors.Is(err, fsx.ErrLocked) {
			return nil, fmt.Errorf("Another syp is already using %s.", dir)
		}
		return nil, fmt.Errorf("lock the data folder %s: %w", dir, err)
	}
	db, err := openDB(dir, ms, unlock)
	if err != nil {
		unlock()
		return nil, err
	}
	return db, nil
}

// openDB opens the database once the data folder is ours. The schema version is read before any
// pragma runs, so a database from a newer program is refused with its file untouched (L-SAFE-18).
func openDB(dir string, ms []Migration, unlock func() error) (*DB, error) {
	path := filepath.Join(dir, "syp.db")
	write, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	write.SetMaxOpenConns(1) // one writer connection (backend-plan §13)

	var version int
	if err := write.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		write.Close()
		return nil, fmt.Errorf("read the schema version of %s: %w", path, err)
	}
	if newest := newestVersion(ms); version > newest {
		write.Close()
		return nil, fmt.Errorf("This database is from a newer syp (schema %d, this program knows %d). Install the newer version, or restore a backup.", version, newest)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := write.Exec(pragma); err != nil {
			write.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	read, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open %s for reading: %w", path, err)
	}
	read.SetMaxOpenConns(4) // a small read pool beside the writer (backend-plan §13)

	return &DB{dir: dir, write: write, read: read, unlock: unlock, migrations: ms}, nil
}

// Read is the read pool: queries only, up to four at a time, beside the writer.
func (db *DB) Read() *sql.DB { return db.read }

// Tx runs fn in one write transaction on the writer connection. It commits when fn returns nil and
// rolls back otherwise.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Close closes the database and gives the data folder back. It is safe to call more than once.
func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		var errs []error
		if db.read != nil {
			if err := db.read.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close the read pool: %w", err))
			}
		}
		if db.write != nil {
			if err := db.write.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close the writer: %w", err))
			}
		}
		if db.unlock != nil {
			if err := db.unlock(); err != nil {
				errs = append(errs, err)
			}
		}
		db.closeErr = errors.Join(errs...)
	})
	return db.closeErr
}
