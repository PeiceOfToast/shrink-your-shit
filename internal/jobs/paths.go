// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "time"

// PathMapping is a remote worker's shared folder (V7c): the server's folder Server is the worker's
// Worker, read or read_write; Test is "" (untested), "pending", "passed" or "failed".
type PathMapping struct {
	Server, Worker, Mode            string
	Test, TestedAt, Detail, Pending string // Pending: a pending test's deadline (RFC 3339)
}

// PathMappings are worker's mappings, by server folder.
func (s *Store) PathMappings(worker string) ([]PathMapping, error) {
	rows, err := s.db.Query(`SELECT server, worker, mode, test, tested_at, detail, pending_until FROM worker_paths WHERE worker_id=? ORDER BY server`, worker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PathMapping{}
	for rows.Next() {
		var m PathMapping
		if err := rows.Scan(&m.Server, &m.Worker, &m.Mode, &m.Test, &m.TestedAt, &m.Detail, &m.Pending); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetPathMappings replaces worker's mappings with list (orch-lead 15:17Z): one whose server, worker and
// mode are all unchanged keeps its test; any other is untested, and isn't used until it passes.
func (s *Store) SetPathMappings(worker string, list []PathMapping) error {
	old, err := s.PathMappings(worker)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM worker_paths WHERE worker_id=?`, worker); err != nil {
		return err
	}
	for _, m := range list {
		keep := PathMapping{Server: m.Server, Worker: m.Worker, Mode: m.Mode}
		for _, o := range old {
			if o.Server == m.Server && o.Worker == m.Worker && o.Mode == m.Mode {
				keep = o
			}
		}
		if _, err := tx.Exec(`INSERT INTO worker_paths(worker_id, server, worker, mode, test, tested_at, detail, pending_until) VALUES (?,?,?,?,?,?,?,?)`,
			worker, keep.Server, keep.Worker, keep.Mode, keep.Test, keep.TestedAt, keep.Detail, keep.Pending); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StartPathTest marks worker's mapping of server pending until deadline (ErrNotFound: no such mapping).
func (s *Store) StartPathTest(worker, server string, deadline time.Time) error {
	r, err := s.db.Exec(`UPDATE worker_paths SET test='pending', detail='', pending_until=? WHERE worker_id=? AND server=?`,
		deadline.UTC().Format(time.RFC3339Nano), worker, server)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// EndPathTest records a test's result ("passed" or "failed", with words), if it is still pending.
func (s *Store) EndPathTest(worker, server, result, detail string, now time.Time) (bool, error) {
	r, err := s.db.Exec(`UPDATE worker_paths SET test=?, detail=?, tested_at=?, pending_until='' WHERE worker_id=? AND server=? AND test='pending'`,
		result, detail, now.UTC().Format(time.RFC3339), worker, server)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}

// OverduePathTests are the pending tests whose deadline passed (a worker that didn't answer, or a
// restart in between), as worker id and server folder.
func (s *Store) OverduePathTests(now time.Time) ([][2]string, error) {
	rows, err := s.db.Query(`SELECT worker_id, server FROM worker_paths WHERE test='pending' AND pending_until<?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var w, sv string
		if err := rows.Scan(&w, &sv); err != nil {
			return nil, err
		}
		out = append(out, [2]string{w, sv})
	}
	return out, rows.Err()
}
