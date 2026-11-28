// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 10 is the replace journal. Migration 9 is taken by the subtitles change (#854).
func init() {
	store.Register(store.Migration{Version: 10, Name: "replace-journal", Up: createJournal})
}

// Journal states (L-SAFE-8). A row is "moving" from before the first move until its swap is settled: "done" when
// the new file is in place, "rolled_back" when the original is back, "attention" when neither can be shown, and
// "purged" when the trash copy is gone.
const (
	StateMoving     = "moving"
	StateRolledBack = "rolled_back"
	StateAttention  = "attention"
	StateDone       = "done"
	StatePurged     = "purged"
)

func createJournal(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE replace_journal (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER NOT NULL,
		original TEXT NOT NULL,
		trash TEXT NOT NULL,
		staged TEXT NOT NULL,
		final TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('moving', 'rolled_back', 'attention', 'done', 'purged')),
		orig_file_id TEXT NOT NULL, orig_size INTEGER NOT NULL, orig_mtime INTEGER NOT NULL,
		orig_ctime INTEGER NOT NULL, orig_hash TEXT NOT NULL,
		new_file_id TEXT NOT NULL, new_size INTEGER NOT NULL, new_mtime INTEGER NOT NULL, new_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`)
	return err
}

// Journal is the replace journal in the store.
type Journal struct {
	db  *store.DB
	now func() time.Time
}

// NewJournal wraps the store's journal table.
func NewJournal(db *store.DB) *Journal { return &Journal{db: db, now: time.Now} }

// Row is one swap: the paths it touches and what each file was when the swap started.
type Row struct {
	ID       int64
	JobID    int64
	Original string
	Trash    string
	Staged   string
	Final    string
	State    string
	Orig     Ident
	New      Ident
}

// Begin writes the "moving" row before anything moves, and returns its id.
func (j *Journal) Begin(ctx context.Context, r Row) (int64, error) {
	var id int64
	err := j.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO replace_journal
			(job_id, original, trash, staged, final, state,
			 orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
			 new_file_id, new_size, new_mtime, new_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'moving', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.JobID, r.Original, r.Trash, r.Staged, r.Final,
			r.Orig.FileID, r.Orig.Size, r.Orig.MTime, r.Orig.CTime, r.Orig.Hash,
			r.New.FileID, r.New.Size, r.New.MTime, r.New.Hash, j.now().Unix(), j.now().Unix())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// SetState moves a row to a state.
func (j *Journal) SetState(ctx context.Context, id int64, state string) error {
	return j.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE replace_journal SET state = ?, updated_at = ? WHERE id = ?`,
			state, j.now().Unix(), id)
		return err
	})
}

// Moving lists the rows whose swap has not been settled, the ones recovery must decide.
func (j *Journal) Moving(ctx context.Context) ([]Row, error) {
	rows, err := j.db.Read().QueryContext(ctx, `SELECT id, job_id, original, trash, staged, final, state,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash
		FROM replace_journal WHERE state = 'moving' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read the replace journal: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State,
			&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
			&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
