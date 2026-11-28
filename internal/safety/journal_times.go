// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"database/sql"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Migration 15 is the two times the trash needs. trashed_at is when a swap settled with its original in the trash, and
// left_at is when that copy left (purged or restored). A row that was already done gets trashed_at from updated_at. A row
// that had already left has no trashed_at, so InTrashSaving skips it: its entry time was overwritten before this migration.
func init() {
	store.Register(store.Migration{Version: 15, Name: "journal-times", Up: addJournalTimes})
}

func addJournalTimes(tx *sql.Tx) error {
	for _, s := range []string{
		`ALTER TABLE replace_journal ADD COLUMN trashed_at INTEGER`,
		`ALTER TABLE replace_journal ADD COLUMN left_at INTEGER`,
		`UPDATE replace_journal SET trashed_at = updated_at WHERE state = 'done'`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// DaySaving is the saving held in the trash at the end of one local day.
type DaySaving struct {
	Day   string // 2006-01-02 in the location given to InTrashSaving
	Bytes int64
}

// InTrashSaving returns, for each local day from the day of from through the day of to, the saving (original minus new,
// clamped at 0) of every copy in the trash at the end of that day. A copy is in the trash at the end of a day when it
// entered on or before that day and had not left by the day's end. Rows with no trashed_at are skipped.
func (j *Journal) InTrashSaving(ctx context.Context, from, to time.Time, loc *time.Location) ([]DaySaving, error) {
	first := startOfLocalDay(from.In(loc), loc)
	last := startOfLocalDay(to.In(loc), loc)
	rows, err := j.db.Read().QueryContext(ctx, `SELECT trashed_at, left_at, orig_size, new_size FROM replace_journal
		WHERE trashed_at IS NOT NULL AND (left_at IS NULL OR left_at >= ?)`, first.Unix())
	if err != nil {
		return nil, err
	}
	type copyInTrash struct {
		in, left int64
		hasLeft  bool
		saving   int64
	}
	var copies []copyInTrash
	for rows.Next() {
		var in int64
		var left sql.NullInt64
		var orig, now int64
		if err := rows.Scan(&in, &left, &orig, &now); err != nil {
			rows.Close()
			return nil, err
		}
		saving := orig - now
		if saving < 0 {
			saving = 0
		}
		copies = append(copies, copyInTrash{in: in, left: left.Int64, hasLeft: left.Valid, saving: saving})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []DaySaving
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		end := d.AddDate(0, 0, 1).Unix()
		var sum int64
		for _, c := range copies {
			if c.in < end && (!c.hasLeft || c.left >= end) {
				sum += c.saving
			}
		}
		out = append(out, DaySaving{Day: d.Format("2006-01-02"), Bytes: sum})
	}
	return out, nil
}

func startOfLocalDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
