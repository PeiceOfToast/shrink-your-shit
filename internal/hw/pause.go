// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// FailLimit is how many GPU failures in a row pause an encoder family on a worker (L-HW-6).
const FailLimit = 3

// PauseFor is how long a paused family waits before jobs may use it again (L-HW-6).
const PauseFor = time.Hour

func init() {
	store.Register(store.Migration{Version: 7, Name: "hw", Up: createEncoderState})
}

func createEncoderState(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE encoder_state(
		worker       TEXT NOT NULL,
		family       TEXT NOT NULL,
		fails        INTEGER NOT NULL DEFAULT 0,
		paused_until INTEGER,
		last_fell    INTEGER,
		PRIMARY KEY (worker, family))`)
	return err
}

// Pauses keeps each worker's GPU failure count and pause, in the store, so a restart does not forget them.
type Pauses struct {
	db  *store.DB
	now func() time.Time
	bus Emitter
}

// NewPauses makes the pause keeper. now and bus may be nil: the clock is time.Now and nothing is reported.
func NewPauses(db *store.DB, now func() time.Time, bus Emitter) *Pauses {
	if now == nil {
		now = time.Now
	}
	return &Pauses{db: db, now: now, bus: bus}
}

// Record notes one GPU attempt's outcome for a worker's encoder family. A success resets the count and leaves any
// pause to run its time. The FailLimit-th failure in a row starts a pause, and the count starts again. It reports
// whether this call started a pause.
func (p *Pauses) Record(ctx context.Context, worker string, fam encode.Family, ok bool) (bool, error) {
	started := false
	now := p.now().UTC().UnixMilli()
	err := p.db.Tx(ctx, func(tx *sql.Tx) error {
		fails, until, err := read(tx, ctx, worker, fam)
		if err != nil {
			return err
		}
		if ok {
			fails = 0
		} else {
			fails++
			if fails >= FailLimit {
				until = sql.NullInt64{Int64: now + PauseFor.Milliseconds(), Valid: true}
				fails = 0
				started = true
			}
		}
		lastFell := sql.NullInt64{}
		if !ok {
			lastFell = sql.NullInt64{Int64: now, Valid: true}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO encoder_state(worker, family, fails, paused_until, last_fell) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(worker, family) DO UPDATE SET fails = excluded.fails, paused_until = excluded.paused_until,
				last_fell = COALESCE(excluded.last_fell, encoder_state.last_fell)`,
			worker, string(fam), fails, until, lastFell)
		return err
	})
	if err != nil {
		return false, err
	}
	if started && p.bus != nil {
		_ = p.bus.Emit(events.Event{
			Kind: events.KindWarning, Level: events.LevelWarn, Source: "hw", Code: "encoder.paused",
			Message: fmt.Sprintf("%s on %s paused for %d min after %d failures in a row", fam, worker, int(PauseFor.Minutes()), FailLimit),
		})
	}
	return started, nil
}

// Paused reports whether a worker's encoder family is paused now, and until when.
func (p *Pauses) Paused(ctx context.Context, worker string, fam encode.Family) (time.Time, bool, error) {
	var until sql.NullInt64
	err := p.db.Read().QueryRowContext(ctx,
		`SELECT paused_until FROM encoder_state WHERE worker = ? AND family = ?`, worker, string(fam)).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if !until.Valid {
		return time.Time{}, false, nil
	}
	t := time.UnixMilli(until.Int64).UTC()
	return t, p.now().UTC().Before(t), nil
}

// read is the current count and pause of one worker's family; a family never seen reads as zero.
func read(tx *sql.Tx, ctx context.Context, worker string, fam encode.Family) (int, sql.NullInt64, error) {
	var fails int
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT fails, paused_until FROM encoder_state WHERE worker = ? AND family = ?`, worker, string(fam)).Scan(&fails, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, sql.NullInt64{}, nil
	}
	return fails, until, err
}
