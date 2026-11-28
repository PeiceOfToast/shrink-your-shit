// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// ErrNotFound is a worker id the store does not have.
var ErrNotFound = errors.New("workers: no such worker")

// LocalID is the built-in worker's id: this computer. The jobs' worker_id uses the same name.
const LocalID = "local"

// Hours are the times a worker may take new jobs, as "HH:MM" in the server's local time. Every day is
// alike; From later than To runs overnight (22:00 to 06:00). nil means any time.
type Hours struct {
	From string
	To   string
}

// Inside says whether t is inside the hours. From counts and To does not, so a job is never grabbed
// twice at the boundary of two periods.
func (h Hours) Inside(t time.Time) bool {
	from, err1 := minutes(h.From)
	to, err2 := minutes(h.To)
	if err1 != nil || err2 != nil || from == to {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// check refuses hours the page cannot show: two real times, and different from each other.
func (h Hours) check() error {
	if _, err := minutes(h.From); err != nil {
		return &InvalidError{Field: "hours", Message: "Pick two different times. From later than To runs overnight."}
	}
	if _, err := minutes(h.To); err != nil {
		return &InvalidError{Field: "hours", Message: "Pick two different times. From later than To runs overnight."}
	}
	if h.From == h.To {
		return &InvalidError{Field: "hours", Message: "Pick two different times. From later than To runs overnight."}
	}
	return nil
}

// minutes reads "HH:MM" as the minute of the day. Both digits of each part are required, as page 10
// sends them: time.Parse on its own would accept "9:00".
func minutes(hhmm string) (int, error) {
	if len(hhmm) != 5 || hhmm[2] != ':' {
		return 0, fmt.Errorf("workers: %q is not HH:MM", hhmm)
	}
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Worker is one worker's stored settings. The live parts (its state, the job it is on, its card)
// belong to the api and the hardware detector, not here.
type Worker struct {
	ID      string
	Name    string
	Address string
	Local   bool
	// Slots is the files this worker runs at once.
	Slots int
	// GPUSlots is how many jobs may encode on its graphics card at once; 0 means none.
	GPUSlots int
	// Hours is when it may take new jobs; nil is any time.
	Hours *Hours
	// PrimaryEncoder is the encoder it prefers: "" for automatic, "cpu" for the processor, or a card
	// encoder's name (e.g. "hevc_qsv"). Stored as NULL for automatic.
	PrimaryEncoder string
}

// AllowsWork says whether the worker may take a new job at t.
func (w Worker) AllowsWork(t time.Time) bool {
	return w.Hours == nil || w.Hours.Inside(t)
}

// InvalidError is a setting the page refuses, with the field it belongs to.
type InvalidError struct {
	Field   string
	Message string
}

func (e *InvalidError) Error() string { return e.Message }

// Store reads and writes the workers' settings.
type Store struct {
	db *store.DB
}

// Open returns the store over db.
func Open(db *store.DB) *Store { return &Store{db: db} }

// List returns every worker, local first.
func (s *Store) List(ctx context.Context) ([]Worker, error) {
	rows, err := s.db.Read().QueryContext(ctx, `
		SELECT id, name, address, local, slots, gpu_slots, hours_from, hours_to, primary_encoder
		FROM workers ORDER BY local DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Worker{}
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Get returns worker id.
func (s *Store) Get(ctx context.Context, id string) (Worker, error) {
	w, err := scanWorker(s.db.Read().QueryRowContext(ctx, `
		SELECT id, name, address, local, slots, gpu_slots, hours_from, hours_to, primary_encoder
		FROM workers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Worker{}, ErrNotFound
	}
	return w, err
}

// scanWorker reads one settings row.
func scanWorker(r interface{ Scan(...any) error }) (Worker, error) {
	var w Worker
	var local int
	var from, to, enc sql.NullString
	if err := r.Scan(&w.ID, &w.Name, &w.Address, &local, &w.Slots, &w.GPUSlots, &from, &to, &enc); err != nil {
		return Worker{}, err
	}
	w.Local = local != 0
	if from.Valid && to.Valid {
		w.Hours = &Hours{From: from.String, To: to.String}
	}
	w.PrimaryEncoder = enc.String
	return w, nil
}

// Save writes every setting of w. The page refuses a count or hours it cannot show, so Save checks them
// again: a route that forgot one still stores nothing impossible.
func (s *Store) Save(ctx context.Context, w Worker) error {
	if w.Slots < 1 {
		return &InvalidError{Field: "slots", Message: "Files at once is at least 1."}
	}
	if w.GPUSlots < 0 {
		return &InvalidError{Field: "gpuSlots", Message: "Graphics slots is 0 or more."}
	}
	var from, to any
	if w.Hours != nil {
		if err := w.Hours.check(); err != nil {
			return err
		}
		from, to = w.Hours.From, w.Hours.To
	}
	var enc any
	if w.PrimaryEncoder != "" {
		enc = w.PrimaryEncoder
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE workers SET name = ?, address = ?, local = ?, slots = ?, gpu_slots = ?,
				hours_from = ?, hours_to = ?, primary_encoder = ? WHERE id = ?`,
			w.Name, w.Address, boolInt(w.Local), w.Slots, w.GPUSlots, from, to, enc, w.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("workers: saving %q: %w", w.ID, ErrNotFound)
		}
		return nil
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
