// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 5 creates the job head row and one row per attempt (backend-plan §4). The lease, workflow revision,
// steps and media columns are here though stage 1 does not fill them, so later stages need no migration.
func init() {
	store.Register(store.Migration{Version: 5, Name: "jobs", Up: createJobs})
}

func createJobs(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_id TEXT NOT NULL,
			library_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL DEFAULT '',
			workflow_rev INTEGER,
			path TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('waiting', 'retry_wait', 'running', 'done', 'failed', 'skipped', 'cancelled')),
			top INTEGER NOT NULL DEFAULT 0,
			queued_at INTEGER NOT NULL,
			queued_by TEXT NOT NULL DEFAULT '',
			started_at INTEGER,
			finished_at INTEGER,
			worker_id TEXT,
			attempt INTEGER NOT NULL DEFAULT 0,
			tries INTEGER NOT NULL DEFAULT 0,
			next_try_at INTEGER,
			lease_id TEXT,
			lease_until INTEGER,
			size_before INTEGER,
			size_after INTEGER,
			outcome TEXT,
			failed_step TEXT,
			reason TEXT,
			next_action TEXT,
			skip_reason TEXT,
			output_path TEXT,
			written_at INTEGER,
			warnings TEXT,
			steps TEXT,
			media TEXT,
			preview INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE job_attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id INTEGER NOT NULL REFERENCES jobs(id),
			attempt INTEGER NOT NULL,
			worker_id TEXT NOT NULL DEFAULT '',
			rung TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			outcome TEXT,
			size_before INTEGER,
			size_after INTEGER,
			output_path TEXT,
			original_action TEXT,
			steps TEXT,
			media TEXT,
			failure TEXT,
			UNIQUE (job_id, attempt)
		)`,
		// One open job per file (L-SCAN-5). A second enqueue for the file is a no-op, not a duplicate.
		`CREATE UNIQUE INDEX jobs_one_open_per_file ON jobs(file_id) WHERE state IN ('waiting', 'retry_wait', 'running')`,
		`CREATE INDEX jobs_claim ON jobs(state, top, queued_at)`,
		`CREATE INDEX jobs_finished ON jobs(finished_at, outcome)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}
