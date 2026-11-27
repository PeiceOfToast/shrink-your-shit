// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"runtime/debug"
)

// store's own migration: the meta table the program's identity lives in (backend-plan §4).
func init() {
	Register(Migration{
		Version: 1,
		Name:    "meta",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
				return err
			}
			id, err := randomHex(8)
			if err != nil {
				return err
			}
			for _, row := range [][2]string{{"install_id", id}, {"app_version", appVersion()}} {
				if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, row[0], row[1]); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

// randomHex is n random bytes as 2n hex characters.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// appVersion is the version this program was built as, or "dev" when the build carries none.
func appVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
