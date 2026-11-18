// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// LocalWorker is the id of the worker in this program (V4a); remote workers come in V7.
const LocalWorker = "local"

// WorkerConfig is what the person sets on a worker (migration 24).
type WorkerConfig struct {
	ID             string
	Name           string
	Tags           []string
	Pool           *string
	GPUSlots       *int    // nil: the default (from the encoder test)
	PrimaryEncoder *string // nil: the default (the first graphics encoder whose test passed, else cpu)
}

// WorkerConfigOf is worker id's settings; the local worker's defaults when it has none saved.
func (s *Store) WorkerConfigOf(id string) (WorkerConfig, error) {
	c := WorkerConfig{ID: id, Name: "This computer", Tags: []string{}}
	var tags string
	var pool sql.NullString
	var gpu sql.NullInt64
	var prim sql.NullString
	err := s.db.QueryRow(`SELECT name, tags, pool, gpu_slots, primary_encoder FROM workers WHERE id=?`, id).Scan(&c.Name, &tags, &pool, &gpu, &prim)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	json.Unmarshal([]byte(tags), &c.Tags)
	if pool.Valid {
		c.Pool = &pool.String
	}
	if gpu.Valid {
		n := int(gpu.Int64)
		c.GPUSlots = &n
	}
	if prim.Valid {
		c.PrimaryEncoder = &prim.String
	}
	return c, nil
}

// SetWorkerConfig saves c.
func (s *Store) SetWorkerConfig(c WorkerConfig) error {
	if c.Tags == nil {
		c.Tags = []string{}
	}
	tags, _ := json.Marshal(c.Tags)
	_, err := s.db.Exec(`INSERT INTO workers(id, name, tags, pool, gpu_slots, primary_encoder) VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, tags=excluded.tags, pool=excluded.pool, gpu_slots=excluded.gpu_slots, primary_encoder=excluded.primary_encoder`,
		c.ID, c.Name, string(tags), c.Pool, c.GPUSlots, c.PrimaryEncoder)
	return err
}

// workerRecord is the record whose on/off flag worker id follows: a paired remote worker's own, else the
// local one (this program's pool: "builtin", "builtin-2", and tests' names).
const workerRecordSQL = `COALESCE((SELECT id FROM workers WHERE id=? AND kind='remote'), 'local')`

// WorkerEnabled reports whether worker id is on (user review 11-12 item 2).
func (s *Store) WorkerEnabled(id string) bool {
	var on bool
	if err := s.db.QueryRow(`SELECT enabled FROM workers WHERE id=`+workerRecordSQL, id).Scan(&on); err != nil {
		return true
	}
	return on
}

// SetWorkerEnabled turns worker id on or off: off, it takes no new job (its running ones finish, or the
// caller stops them). ErrNotFound: no such worker.
func (s *Store) SetWorkerEnabled(id string, on bool) error {
	was := s.Paused()
	r, err := s.db.Exec(`UPDATE workers SET enabled=? WHERE id=?`, on, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.pausedChanged(was)
}
