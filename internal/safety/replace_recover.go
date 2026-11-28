// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"fmt"
	"os"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// RecoverReport counts what recovery did with the unsettled swaps it found.
type RecoverReport struct {
	Done       int // the new file is at its name and the original is in the trash (or purged)
	RolledBack int // the original is back at its name, or put back there
	Attention  int // neither file can be shown in place: the row waits for a person, and nothing is removed
	// Restored lists the job of each restore that recovery finished, so the caller writes its History line (review N4).
	Restored []int64
}

// Recover settles the swaps a crash left in "moving" (L-SAFE-9). It decides from identity, never from names: a file is
// the original or the new file by its size, mtime and sample hash, wherever it is. It never overwrites a name, and a
// stranger's file at the original's name is left alone. A row it cannot settle becomes "attention" and is not purged.
func Recover(ctx context.Context, j *Journal) (RecoverReport, error) {
	var rep RecoverReport
	if err := recoverPurged(ctx, j); err != nil {
		return rep, err
	}
	rows, err := j.Moving(ctx)
	if err != nil {
		return rep, err
	}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		// A restore that stopped partway is settled from where its files are (see settleRestore).
		state, restored := settleRestore(r)
		if !restored && r.Kind == "restore" && matches(r.Original, r.Orig) && !matches(r.Final, r.New) {
			// A restore whose converted copy is already gone and whose original is back: the restore finished, and
			// only the record was left (review M2).
			state, restored = StateRolledBack, true
		}
		if !restored {
			state = settle(r)
			if state == StateRolledBack {
				// Put the original back when it is in the trash and its name is free: nothing else is touched.
				if putBackOriginal(r) {
					state = StateRolledBack
				} else {
					state = StateAttention
				}
			}
		}
		if err := j.SetState(ctx, r.ID, state); err != nil {
			return rep, err
		}
		if restored && state == StateRolledBack {
			rep.Restored = append(rep.Restored, r.JobID)
		}
		switch state {
		case StateDone:
			rep.Done++
		case StateRolledBack:
			rep.RolledBack++
		default:
			rep.Attention++
		}
	}
	return rep, nil
}

// settle decides what a row's files say, without moving anything.
func settle(r Row) string {
	newAtFinal := matches(r.Final, r.New)
	origInPlace := matches(r.Original, r.Orig)
	origInTrash := matches(r.Trash, r.Orig)
	switch {
	case newAtFinal && origInPlace:
		// Two copies of the original and the new file at their names cannot both be right: leave both for a person.
		return StateAttention
	case newAtFinal && (origInTrash || !exists(r.Trash)):
		// The swap reached its last move: the new file is at its name, the original is in the trash or purged.
		return StateDone
	case origInPlace:
		// Nothing left the name, or the original came back: the swap did not happen.
		return StateRolledBack
	case origInTrash:
		// The original is in the trash and the new file did not reach its name: put the original back.
		return StateRolledBack
	default:
		// Neither file can be shown: the row waits for a person and nothing is removed (L-SAFE-9).
		return StateAttention
	}
}

// putBackOriginal moves the original from the trash to its name when the name is free. A stranger at the name, or a
// failed move, returns false: the original stays in the trash and the row is "attention".
func putBackOriginal(r Row) bool {
	if matches(r.Original, r.Orig) {
		return true
	}
	if exists(r.Original) {
		return false
	}
	if !matches(r.Trash, r.Orig) {
		return true // the original is not in the trash, so there is nothing to move back
	}
	return fsx.RenameNoReplace(r.Trash, r.Original) == nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// recoverPurged is the startup pass for a sweep that crashed between its claim (done to purged) and its revert: a purged
// row whose trash file is still there and is the recorded original goes back to done, so the next sweep can take it
// and the original is not lost (review M3).
func recoverPurged(ctx context.Context, j *Journal) error {
	rows, err := j.db.Read().QueryContext(ctx, `SELECT id, job_id, original, trash, staged, final, state, kind,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash
		FROM replace_journal WHERE state = 'purged'`)
	if err != nil {
		return fmt.Errorf("read the purged rows: %w", err)
	}
	var found []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State, &r.Kind,
			&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
			&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash); err != nil {
			rows.Close()
			return err
		}
		found = append(found, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range found {
		if matches(r.Trash, r.Orig) {
			if err := j.SetState(ctx, r.ID, StateDone); err != nil {
				return err
			}
		}
	}
	return nil
}
