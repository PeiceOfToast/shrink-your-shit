// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workers

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 13 creates the workers' settings (page 10) and the one row this program runs: the built-in
// worker "local", which takes every library and converts on this computer. Its defaults are one file at
// once and one graphics slot; a machine with no card never uses the card's place, so the default needs
// no knowledge of the hardware.
func init() {
	store.Register(store.Migration{Version: 13, Name: "workers", Up: createWorkers})
}

func createWorkers(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE workers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			address TEXT NOT NULL DEFAULT '',
			local INTEGER NOT NULL DEFAULT 0,
			slots INTEGER NOT NULL DEFAULT 1,
			gpu_slots INTEGER NOT NULL DEFAULT 1,
			hours_from TEXT,
			hours_to TEXT,
			primary_encoder TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	slots, err := newWorkerSlots(tx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO workers(id, name, address, local, slots, gpu_slots)
		VALUES ('local', 'This computer', '', 1, ?, 1)`, slots)
	return err
}

// newWorkerSlots is the files-at-once a new worker starts with: page 11's processing.parallelJobs,
// clamped to the same 1 to 8. After creation the worker's own row is the authority, and a later page
// 11 change does not move it (the S3-4 brief's reading). The settings table is migration 12, present
// on every real install and absent only in a test that registers this migration alone.
func newWorkerSlots(tx *sql.Tx) (int, error) {
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'app_settings'`).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 1, nil
	}
	var value string
	err := tx.QueryRow(`SELECT value FROM app_settings WHERE key = 'processing.parallelJobs'`).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 1, nil
	case err != nil:
		return 0, err
	}
	var slots int
	if json.Unmarshal([]byte(value), &slots) != nil || slots < 1 || slots > 8 {
		return 1, nil
	}
	return slots, nil
}
