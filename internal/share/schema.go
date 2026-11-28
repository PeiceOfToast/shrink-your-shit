// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package share is SMB and NFS shares (stage 3 item 7): the addresses a person can type, the
// password kept sealed under the access token's key, and the mount actions the API asks for. The
// privileged side (mounting as root) lives in the helper, behind the Mounter interface.
package share

import (
	"database/sql"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 16 is the shares table and the two library columns share-pause will use (S3-7). The
// columns are here though slice 5 sets them, so the library's status, claim and scan rules need no
// later migration. Migration 14 and 15 are the journal's; 13 is the workers.
func init() {
	store.Register(store.Migration{Version: 16, Name: "shares", Up: createShares})
}

func createShares(tx *sql.Tx) error {
	stmts := []string{
		// The password is sealed under secret.key, the same key and code as the access token. The
		// normalized address is the full source plus the subfolder; sub keeps the folder a library
		// watches on its own, because the helper mounts only the source.
		`CREATE TABLE shares (
			id              INTEGER PRIMARY KEY,
			name            TEXT    NOT NULL,
			type            TEXT    NOT NULL CHECK (type IN ('smb', 'nfs')),
			address         TEXT    NOT NULL,
			sub             TEXT    NOT NULL DEFAULT '',
			username        TEXT    NOT NULL DEFAULT '',
			domain          TEXT    NOT NULL DEFAULT '',
			guest           INTEGER NOT NULL DEFAULT 0,
			password_sealed BLOB,
			connected       INTEGER NOT NULL DEFAULT 0,
			point           TEXT    NOT NULL DEFAULT '',
			since           INTEGER,
			reason          TEXT    NOT NULL DEFAULT '',
			created         INTEGER NOT NULL
		)`,
		`ALTER TABLE libraries ADD COLUMN share_paused INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE libraries ADD COLUMN share_down_at INTEGER`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
