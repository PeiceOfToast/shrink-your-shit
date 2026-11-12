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
	ID       string
	Name     string
	Tags     []string
	Pool     *string
	GPUSlots *int // nil: the default (from the encoder test)
}

// WorkerConfigOf is worker id's settings; the local worker's defaults when it has none saved.
func (s *Store) WorkerConfigOf(id string) (WorkerConfig, error) {
	c := WorkerConfig{ID: id, Name: "This computer", Tags: []string{}}
	var tags string
	var pool sql.NullString
	var gpu sql.NullInt64
	err := s.db.QueryRow(`SELECT name, tags, pool, gpu_slots FROM workers WHERE id=?`, id).Scan(&c.Name, &tags, &pool, &gpu)
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
	return c, nil
}

// SetWorkerConfig saves c.
func (s *Store) SetWorkerConfig(c WorkerConfig) error {
	if c.Tags == nil {
		c.Tags = []string{}
	}
	tags, _ := json.Marshal(c.Tags)
	_, err := s.db.Exec(`INSERT INTO workers(id, name, tags, pool, gpu_slots) VALUES (?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, tags=excluded.tags, pool=excluded.pool, gpu_slots=excluded.gpu_slots`,
		c.ID, c.Name, string(tags), c.Pool, c.GPUSlots)
	return err
}
