// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// ErrNotRunnable is RunAgain refusing a job that is not finished as done or skipped. State is the state it is in.
type ErrNotRunnable struct{ State State }

func (e *ErrNotRunnable) Error() string {
	return "jobs: a " + string(e.State) + " job does not run again"
}

// RunAgain puts a finished job (done or skipped) back to waiting on the same row, to convert the file at its path as it
// is now, with the workflow as published now. The old output details are cleared, as Retry clears them (L-Q-3). The
// file's one open job is this row, so a scan that finds the file does not queue it again (Enqueue answers with it).
func (q *Queue) RunAgain(ctx context.Context, id int64) error {
	var libraryID int64
	var attempt int
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		var fileID int64
		var state string
		if err := tx.QueryRow(
			`SELECT file_id, library_id, attempt, state FROM jobs WHERE id = ?`, id,
		).Scan(&fileID, &libraryID, &attempt, &state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if s := State(state); s != Done && s != Skipped {
			return &ErrNotRunnable{State: s}
		}
		var other int64
		err := tx.QueryRow(
			`SELECT id FROM jobs WHERE file_id = ? AND id <> ? AND state IN (`+marks(len(openStates))+`) LIMIT 1`,
			append([]any{fileID, id}, stateArgs(openStates)...)...,
		).Scan(&other)
		if err == nil {
			return &ErrFileBusy{JobID: other}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return move(tx, id, verbRerun, Waiting, change{
			set: `, tries = 0, next_try_at = NULL, finished_at = NULL, outcome = NULL,
				failed_step = NULL, failure_code = NULL, reason = NULL, next_action = NULL, skip_reason = NULL,
				output_path = NULL, size_before = NULL, size_after = NULL, workflow_rev = NULL, top = 0`,
		})
	})
	if err != nil {
		return err
	}
	return q.emit(events.KindQueued, events.LevelInfo, id, attempt, libraryID, "run_again", "queued to run again")
}
