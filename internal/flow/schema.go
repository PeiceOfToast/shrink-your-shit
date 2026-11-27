// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"database/sql"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 6 is the workflow tables (backend-plan §4): the workflow row with its draft and
// published pointer, and the revisions, which are never changed once written.
func init() {
	store.Register(store.Migration{Version: 6, Name: "flow", Up: createFlowTables})
	store.Register(store.Migration{Version: 8, Name: "flow-draft-rev", Up: addDraftRev})
}

// addDraftRev gives the draft its own revision: the number the editor holds and sends back as
// baseRev, so a save from a page that has fallen behind is refused instead of quietly
// overwriting a newer draft. It moves on with every draft save and every publish.
func addDraftRev(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE workflows ADD COLUMN draft_rev INTEGER NOT NULL DEFAULT 0`)
	return err
}

func createFlowTables(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE workflows (
			id             INTEGER PRIMARY KEY,
			name           TEXT    NOT NULL,
			description    TEXT    NOT NULL DEFAULT '',
			kind           TEXT    NOT NULL DEFAULT 'video',
			made_with      TEXT    NOT NULL DEFAULT '',
			answers        TEXT,
			draft          TEXT,
			draft_base_rev INTEGER NOT NULL DEFAULT 0,
			current_rev    INTEGER,
			created_at     INTEGER NOT NULL,
			updated_at     INTEGER NOT NULL,
			deleted_at     INTEGER
		)`,
		`CREATE TABLE workflow_revisions (
			workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
			rev         INTEGER NOT NULL,
			flow        TEXT    NOT NULL,
			created_at  INTEGER NOT NULL,
			PRIMARY KEY (workflow_id, rev)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
