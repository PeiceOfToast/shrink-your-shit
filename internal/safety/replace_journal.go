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
	// Kind is "replace" for a swap and "restore" for a restore (migration 14). Recovery reads it to tell a finished
	// restore from a rolled-back replace, which can leave the same files behind (review M2).
	Kind string
	Orig Ident
	New  Ident
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
// stateTimesSQL sets the two trash times with a state change (migration 15): trashed_at is the first time a swap settled
// with its original in the trash, and it is kept across a purge revert. left_at is when the copy left (purged or
// restored), and a return to done clears it. Every state write goes through it: SetState and claim.
const stateTimesSQL = `trashed_at = CASE WHEN ? = 'done' THEN COALESCE(trashed_at, ?) ELSE trashed_at END,
		left_at = CASE WHEN ? IN ('purged', 'rolled_back') THEN COALESCE(left_at, ?)
		               WHEN ? = 'done' THEN NULL ELSE left_at END`

// stateTimesArgs are stateTimesSQL's five placeholders for a move to state at now.
func stateTimesArgs(state string, now int64) []any {
	return []any{state, now, state, now, state}
}

// SetState moves a row to a state.
func (j *Journal) SetState(ctx context.Context, id int64, state string) error {
	now := j.now().Unix()
	return j.db.Tx(ctx, func(tx *sql.Tx) error {
		args := append([]any{state, now}, stateTimesArgs(state, now)...)
		_, err := tx.ExecContext(ctx, `UPDATE replace_journal SET state = ?, updated_at = ?, `+stateTimesSQL+`
			WHERE id = ?`, append(args, id)...)
		return err
	})
}

// Moving lists the rows whose swap has not been settled, the ones recovery must decide.
func (j *Journal) Moving(ctx context.Context) ([]Row, error) {
	rows, err := j.db.Read().QueryContext(ctx, `SELECT id, job_id, original, trash, staged, final, state, kind,
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
		if err := rows.Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State, &r.Kind,
			&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
			&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
