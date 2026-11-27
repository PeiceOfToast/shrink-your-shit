// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"database/sql"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

func init() {
	store.Register(store.Migration{Version: 4, Name: "library", Up: createLibraryTables})
}

// createLibraryTables is migration 4 (backend-plan §4 "libraries" and "files"). share_id stays
// nullable because shares arrive in stage 3; workflow_id is nullable because a new library has no
// workflow yet. The file row keeps the probe summary and the last settle look, so a scan is a diff
// against the catalog rather than another walk of the folder.
func createLibraryTables(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE libraries (
			id          INTEGER PRIMARY KEY,
			name        TEXT    NOT NULL,
			path        TEXT    NOT NULL,
			share_id    INTEGER,
			workflow_id INTEGER,
			watch       TEXT    NOT NULL DEFAULT 'daily',
			paused      INTEGER NOT NULL DEFAULT 0,
			next_scan_at INTEGER,
			last_scan   TEXT
		)`,
		`CREATE UNIQUE INDEX libraries_path ON libraries(path)`,
		`CREATE INDEX libraries_due ON libraries(paused, next_scan_at)`,
		`CREATE TABLE files (
			id          INTEGER PRIMARY KEY,
			library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			rel_path    TEXT    NOT NULL,
			size        INTEGER NOT NULL DEFAULT 0,
			mtime       INTEGER NOT NULL DEFAULT 0,
			file_id     TEXT    NOT NULL DEFAULT '',
			settle      TEXT,
			probe       TEXT,
			state       TEXT    NOT NULL DEFAULT 'still_changing',
			skip_reason TEXT    NOT NULL DEFAULT '',
			last_job_id INTEGER,
			UNIQUE (library_id, rel_path)
		)`,
		`CREATE INDEX files_library_state ON files(library_id, state)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
