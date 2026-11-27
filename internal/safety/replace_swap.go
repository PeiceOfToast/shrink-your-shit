// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// SwapPlan is one replace: the original, the new file written beside the job, and the name the new file takes.
type SwapPlan struct {
	JobID    int64
	Original string // the file being replaced
	Staged   string // the new file the job wrote
	Final    string // the name the new file takes: the original's stem with the new extension
	KeepDays int    // 0 deletes the trash copy right after a verified swap (L-SAFE-6)
}

// SwapResult says where the original went and whether the trash copy was kept.
type SwapResult struct {
	Trash  string
	Final  string
	Purged bool
}

// ErrSwapFailed means the new file did not reach its name; the original was put back, so nothing is lost.
var ErrSwapFailed = errors.New("safety: the new file could not be placed; the original is back in place")

// ErrPutBackFailed means the new file did not reach its name and the original could not be put back. It is never
// reported as done: the row is "attention" and the original is still in the trash.
var ErrPutBackFailed = errors.New("safety: the new file could not be placed and the original could not be put back")

// crashHook is a test hook: it runs after each numbered step of a swap, so a test can kill the process there
// (L-SAFE-8's kill test). It is nil outside tests.
var crashHook func(step int)

// hookAfterOriginalMoved is a test hook that runs once the original is in the trash, to change the folder in the
// window between two moves.
var hookAfterOriginalMoved = func() {}

// hookBeforeOriginalMove is a test hook that runs after the source check and before the move, the window in which a
// stranger's file could take the name (review #865).
var hookBeforeOriginalMove = func() {}

func crashPoint(step int) {
	if crashHook != nil {
		crashHook(step)
	}
}

// Swap replaces the original with the staged file. The steps follow L-SAFE-8: the trash folder, the identities, the
// trash path, the journal row, the original into the trash, the new file into the name, then the row is settled.
// The source is checked against its identity before each move (L-SAFE-5); a move never overwrites (L-SAFE-10).
func Swap(ctx context.Context, j *Journal, p SwapPlan) (SwapResult, error) {
	libDir := filepath.Dir(p.Original)
	trashDir, err := ensureTrash(libDir) // step 1
	if err != nil {
		return SwapResult{}, err
	}
	crashPoint(1)

	origID, err := identify(p.Original) // step 2
	if err != nil {
		return SwapResult{}, fmt.Errorf("identify the original: %w", err)
	}
	if origID.Links > 1 {
		return SwapResult{}, ErrSharedSource
	}
	newID, err := identify(p.Staged)
	if err != nil {
		return SwapResult{}, fmt.Errorf("identify the new file: %w", err)
	}
	afterIdentify(p.Original)
	crashPoint(2)

	trash, err := trashPath(trashDir, p.Original, p.JobID) // step 3
	if err != nil {
		return SwapResult{}, err
	}
	crashPoint(3)

	row := Row{JobID: p.JobID, Original: p.Original, Trash: trash, Staged: p.Staged, Final: p.Final, Orig: origID, New: newID}
	id, err := j.Begin(ctx, row) // step 4: the journal row is written before anything moves
	if err != nil {
		return SwapResult{}, fmt.Errorf("write the replace journal: %w", err)
	}
	crashPoint(4)

	// Step 5: the original into the trash, after the source check.
	if err := unchanged(p.Original, origID); err != nil {
		_ = j.SetState(ctx, id, StateRolledBack)
		return SwapResult{}, err
	}
	hookBeforeOriginalMove()
	if err := fsx.RenameNoReplace(p.Original, trash); err != nil {
		_ = j.SetState(ctx, id, StateRolledBack)
		return SwapResult{}, fmt.Errorf("move the original to the trash: %w", err)
	}
	// The check above ran on the path, so a file put at the name in the window before the move would be the one
	// moved. The trash copy is identified against the recorded original: a mismatch is put back (no-replace) and the
	// row waits for a person. The stranger is never lost or replaced (review #865).
	if got, err := identify(trash); err != nil || !sameIdent(got, origID) {
		if rerr := fsx.RenameNoReplace(trash, p.Original); rerr != nil {
			_ = j.SetState(ctx, id, StateAttention)
			return SwapResult{}, fmt.Errorf("%w: the file at the name was not the original, and it could not be put back to the name (%v)", ErrPutBackFailed, rerr)
		}
		_ = j.SetState(ctx, id, StateAttention)
		return SwapResult{}, ErrSourceChanged
	}
	hookAfterOriginalMoved()
	crashPoint(5)

	// Step 6: the new file into the name, after its own check. A failure puts the original back.
	if err := unchanged(p.Staged, newID); err != nil {
		return SwapResult{}, restoreOriginal(ctx, j, id, row, false, err)
	}
	if err := fsx.RenameNoReplace(p.Staged, p.Final); err != nil {
		return SwapResult{}, restoreOriginal(ctx, j, id, row, false, fmt.Errorf("%w: %v", ErrSwapFailed, err))
	}
	if !matches(p.Final, newID) {
		return SwapResult{}, restoreOriginal(ctx, j, id, row, true, ErrSwapFailed)
	}
	crashPoint(6)

	// Step 7: settle the row. With keep 0 the trash copy goes now that the swap is verified.
	res := SwapResult{Trash: trash, Final: p.Final}
	if p.KeepDays == 0 {
		if err := os.Remove(trash); err != nil {
			// The swap is verified; the copy stays and the row stays "done" for the sweep to report.
			_ = j.SetState(ctx, id, StateDone)
			return res, nil
		}
		res.Purged = true
		if err := j.SetState(ctx, id, StatePurged); err != nil {
			return res, err
		}
	} else if err := j.SetState(ctx, id, StateDone); err != nil {
		return res, err
	}
	crashPoint(7)
	return res, nil
}

// restoreOriginal undoes a failed placement: the new file goes back to its staged name when it was placed, and the original
// returns from the trash to its name. A put-back that fails is "attention" with the original still in the trash,
// and the error says so; it is never reported as done.
func restoreOriginal(ctx context.Context, j *Journal, id int64, row Row, newPlaced bool, cause error) error {
	if newPlaced {
		if err := fsx.RenameNoReplace(row.Final, row.Staged); err != nil {
			_ = j.SetState(ctx, id, StateAttention)
			return fmt.Errorf("%w (%v)", ErrPutBackFailed, cause)
		}
	}
	if err := fsx.RenameNoReplace(row.Trash, row.Original); err != nil {
		_ = j.SetState(ctx, id, StateAttention)
		return fmt.Errorf("%w (%v)", ErrPutBackFailed, cause)
	}
	_ = j.SetState(ctx, id, StateRolledBack)
	return cause
}

// sameIdent is the recorded identity of a file: the same file, size, mtime and sample hash.
func sameIdent(got, want Ident) bool {
	return got.FileID == want.FileID && got.Size == want.Size && got.MTime == want.MTime && got.Hash == want.Hash
}
