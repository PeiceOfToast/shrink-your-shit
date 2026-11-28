// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 12 is the app settings table. Migration 11 is taken by the failure code (#789).
func init() {
	store.Register(store.Migration{Version: 12, Name: "app-settings", Up: createSettings})
}

func createSettings(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

// KeepOriginalsKey is the settings key for processing.keepOriginalsDays (page 11).
const KeepOriginalsKey = "processing.keepOriginalsDays"

// DefaultKeepDays is how long a replaced original stays in the trash when the setting is not set (L-SAFE-6).
const DefaultKeepDays = 7

// MaxKeepDays is the most a setting may keep an original.
const MaxKeepDays = 365

// ErrNotFound means the trash has no such entry.
var ErrNotFound = errors.New("safety: no such trash entry")

// Refusals of a restore. Each leaves the library as it was: the original stays in the trash and the converted file is
// not touched.
var (
	// ErrOriginalGone means the original is no longer in the trash as recorded (purged, or changed).
	ErrOriginalGone = errors.New("safety: the original is no longer in the trash")
	// ErrConvertedChanged means the converted file at its name is not the file the replace recorded.
	ErrConvertedChanged = errors.New("safety: the converted file changed or is gone, so the original was not restored")
	// ErrNameTaken means something is already at the original's name.
	ErrNameTaken = errors.New("safety: something is already at the original's name")
)

// KeepDays reads processing.keepOriginalsDays from the store, DefaultKeepDays when it was never set.
func (j *Journal) KeepDays(ctx context.Context) (int, error) {
	var v string
	err := j.db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, KeepOriginalsKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultKeepDays, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read keepOriginalsDays: %w", err)
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 || n > MaxKeepDays {
		return DefaultKeepDays, nil
	}
	return n, nil
}

// SetKeepDays stores processing.keepOriginalsDays. Values outside 0..365 are refused.
func (j *Journal) SetKeepDays(ctx context.Context, days int) error {
	if days < 0 || days > MaxKeepDays {
		return fmt.Errorf("safety: keep originals must be 0 to %d days", MaxKeepDays)
	}
	return j.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, KeepOriginalsKey, fmt.Sprint(days))
		return err
	})
}

// DB is the store the journal writes to, for callers that read beside it.
func (j *Journal) DB() *store.DB { return j.db }

// TrashEntry is one replaced original still in the trash: the row it came from and when it went there.
type TrashEntry struct {
	ID       int64
	JobID    int64
	Original string
	Trash    string
	Final    string
	When     time.Time
}

// trashEntryFrom turns a done row into a trash entry.
func trashEntryFrom(r Row, when int64) TrashEntry {
	return TrashEntry{ID: r.ID, JobID: r.JobID, Original: r.Original, Trash: r.Trash, Final: r.Final, When: time.Unix(when, 0)}
}

// doneRows lists the rows whose swap settled with the original still in the trash, newest first.
func (j *Journal) doneRows(ctx context.Context) ([]Row, []int64, error) {
	rows, err := j.db.Read().QueryContext(ctx, `SELECT id, job_id, original, trash, staged, final, state,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash, updated_at
		FROM replace_journal WHERE state = 'done' ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, nil, fmt.Errorf("read the trash: %w", err)
	}
	defer rows.Close()
	var out []Row
	var when []int64
	for rows.Next() {
		var r Row
		var at int64
		if err := rows.Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State,
			&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
			&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash, &at); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
		when = append(when, at)
	}
	return out, when, rows.Err()
}

// Trash lists the originals still in the trash, newest first. An entry whose trash copy is not the recorded file is
// left out: it cannot be restored, and the sweep does not remove it.
func (j *Journal) Trash(ctx context.Context) ([]TrashEntry, error) {
	rows, when, err := j.doneRows(ctx)
	if err != nil {
		return nil, err
	}
	var out []TrashEntry
	for i, r := range rows {
		if matches(r.Trash, r.Orig) {
			out = append(out, trashEntryFrom(r, when[i]))
		}
	}
	return out, nil
}

// Entry reads one trash row by its journal id, whatever its state.
func (j *Journal) Entry(ctx context.Context, id int64) (Row, int64, error) {
	var r Row
	var at int64
	err := j.db.Read().QueryRowContext(ctx, `SELECT id, job_id, original, trash, staged, final, state,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash, updated_at
		FROM replace_journal WHERE id = ?`, id).Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State,
		&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
		&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, 0, ErrNotFound
	}
	return r, at, err
}

// AttentionEntry is a row a person must look at, with when it was last written.
type AttentionEntry struct {
	Row  Row
	When time.Time
}

// Attention lists the rows a person must look at: a swap that could not be settled or put back.
func (j *Journal) Attention(ctx context.Context) ([]AttentionEntry, error) {
	rows, err := j.db.Read().QueryContext(ctx, `SELECT id, job_id, original, trash, staged, final, state,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash, updated_at
		FROM replace_journal WHERE state = 'attention' ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("read the attention rows: %w", err)
	}
	defer rows.Close()
	var out []AttentionEntry
	for rows.Next() {
		var r Row
		var at int64
		if err := rows.Scan(&r.ID, &r.JobID, &r.Original, &r.Trash, &r.Staged, &r.Final, &r.State,
			&r.Orig.FileID, &r.Orig.Size, &r.Orig.MTime, &r.Orig.CTime, &r.Orig.Hash,
			&r.New.FileID, &r.New.Size, &r.New.MTime, &r.New.Hash, &at); err != nil {
			return nil, err
		}
		out = append(out, AttentionEntry{Row: r, When: time.Unix(at, 0)})
	}
	return out, rows.Err()
}

// TrashSweepReport says what the daily trash sweep did.
type TrashSweepReport struct {
	Purged int // trash copies past their keep days, removed
	Kept   int // past their keep days, but the trash copy is not the recorded file, so it stays
}

// SweepTrash removes the trash copies older than keepDays. Only a copy that is still exactly the recorded original is
// removed; a row goes to "purged". A row is never moved to "attention" by the sweep (L-SAFE-6).
func SweepTrash(ctx context.Context, j *Journal, now time.Time, keepDays int) (TrashSweepReport, error) {
	var rep TrashSweepReport
	rows, when, err := j.doneRows(ctx)
	if err != nil {
		return rep, err
	}
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour).Unix()
	for i, r := range rows {
		if when[i] > cutoff {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if !matches(r.Trash, r.Orig) {
			rep.Kept++
			continue
		}
		if err := os.Remove(r.Trash); err != nil {
			rep.Kept++
			continue
		}
		if err := j.SetState(ctx, r.ID, StatePurged); err != nil {
			return rep, err
		}
		rep.Purged++
	}
	return rep, nil
}

// hookRestoreStep is a test hook: it runs after each step of a restore, so a test can stop there.
var hookRestoreStep = func(step int) {}

// Restore puts a replaced original back at its name and removes the converted file, journalled so a crash part way
// is settled by Recover (L-SAFE-19). It never overwrites a name: a taken name is refused. Each refusal returns its
// sentinel and changes nothing.
//
// The row goes to "moving" before anything moves. Recover then reads the files: with the converted file still in
// place the swap is "done" again; with it gone and the original in the trash, the original is put back and the row is
// "rolled_back", which History reads as "Original restored".
func Restore(ctx context.Context, j *Journal, id int64) error {
	r, _, err := j.Entry(ctx, id)
	if err != nil {
		return err
	}
	if r.State != StateDone || !matches(r.Trash, r.Orig) {
		return ErrOriginalGone
	}
	if !matches(r.Final, r.New) {
		return ErrConvertedChanged
	}
	// The converted file may sit at the original's own name (same extension); it is removed below, so only a
	// different name is checked here.
	if r.Original != r.Final && exists(r.Original) {
		return ErrNameTaken
	}
	if err := j.SetState(ctx, id, StateMoving); err != nil {
		return err
	}
	hookRestoreStep(1)
	// Step 2: the converted file is moved aside into the trash, not deleted, so a refusal from here on can put it back.
	// It is identified before the move; a file that changed since the check is left where it is.
	trashDir := filepath.Dir(r.Trash)
	holding, err := trashPath(trashDir, r.Final, r.JobID)
	if err != nil {
		_ = j.SetState(ctx, id, StateDone)
		return err
	}
	if !matches(r.Final, r.New) {
		_ = j.SetState(ctx, id, StateDone)
		return ErrConvertedChanged
	}
	if err := fsx.RenameNoReplace(r.Final, holding); err != nil {
		_ = j.SetState(ctx, id, StateDone)
		return fmt.Errorf("move the converted file aside: %w", err)
	}
	hookRestoreStep(2)
	// Step 3: the original returns to its name, a no-replace move. A name taken meanwhile puts the converted file back
	// where it was, so a refused restore changes nothing; if even that fails, the converted file stays in the trash
	// and the row waits for a person. Nothing is deleted on a refusal.
	if err := fsx.RenameNoReplace(r.Trash, r.Original); err != nil {
		taken := exists(r.Original)
		if backErr := fsx.RenameNoReplace(holding, r.Final); backErr != nil {
			_ = j.SetState(ctx, id, StateAttention)
			return fmt.Errorf("the original could not be put back and the converted file is in the trash folder (%s): %w", holding, err)
		}
		_ = j.SetState(ctx, id, StateDone)
		if taken {
			return ErrNameTaken
		}
		return fmt.Errorf("put the original back: %w", err)
	}
	hookRestoreStep(3)
	if !matches(r.Original, r.Orig) {
		_ = j.SetState(ctx, id, StateAttention)
		return ErrOriginalGone
	}
	// Step 4: the original is back and verified, so the converted copy is removed. If that removal fails the original
	// is still restored; the copy stays in the trash and nothing is lost.
	if matches(holding, r.New) {
		_ = os.Remove(holding)
	}
	if err := j.SetState(ctx, id, StateRolledBack); err != nil {
		return fmt.Errorf("restored, but the record could not be saved: %w", err)
	}
	return nil
}

// RestoredSentence is the History line for a restored original.
const RestoredSentence = "Original restored"
