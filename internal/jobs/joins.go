// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
)

// Join requests (E2) are a worker's ask to join from the server's own network, kept in the store so that an allowed
// request survives a server restart. The secret is kept only as its hash. The worker's token is not kept at all: it
// is minted when the worker picks its allow up (PickUpJoin), and only its hash is stored, as for any remote worker.
// An allowed request that expires unpicked removes the worker Allow created, but only if that worker has no jobs:
// a worker that never picked up its allow never had a token, so it can never have called the server.

// JoinRequest is one join request. Times are unix nanoseconds. Decision is "" while waiting, else "allowed" or
// "denied".
type JoinRequest struct {
	ID, SecretHash          string
	Name, OS, Arch, Version string
	Hardware                *string
	APILevel                int
	Capabilities            string
	Address                 string
	Port                    int
	AskedNS, ExpiresNS      int64
	Decision                string
	WorkerID, WorkerName    string
}

const joinCols = `id, secret_hash, name, os, arch, version, hardware, api_level, capabilities, address, port, asked_ns, expires_ns, decision, worker_id, worker_name`

func scanJoin(sc interface{ Scan(...any) error }) (JoinRequest, error) {
	var r JoinRequest
	var hw sql.NullString
	err := sc.Scan(&r.ID, &r.SecretHash, &r.Name, &r.OS, &r.Arch, &r.Version, &hw, &r.APILevel, &r.Capabilities,
		&r.Address, &r.Port, &r.AskedNS, &r.ExpiresNS, &r.Decision, &r.WorkerID, &r.WorkerName)
	if hw.Valid {
		h := hw.String
		r.Hardware = &h
	}
	return r, err
}

// AddJoin stores a new waiting request.
func (s *Store) AddJoin(r JoinRequest) error {
	var hw any
	if r.Hardware != nil {
		hw = *r.Hardware
	}
	_, err := s.db.Exec(`INSERT INTO join_requests(`+joinCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,'','','')`,
		r.ID, r.SecretHash, r.Name, r.OS, r.Arch, r.Version, hw, r.APILevel, r.Capabilities, r.Address, r.Port, r.AskedNS, r.ExpiresNS)
	return err
}

// Join is request id (ErrNotFound: no such request, or it was pruned).
func (s *Store) Join(id string) (JoinRequest, error) {
	r, err := scanJoin(s.db.QueryRow(`SELECT `+joinCols+` FROM join_requests WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// JoinsWaiting lists the requests still unanswered, newest first.
func (s *Store) JoinsWaiting() ([]JoinRequest, error) {
	rows, err := s.db.Query(`SELECT ` + joinCols + ` FROM join_requests WHERE decision='' ORDER BY asked_ns DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JoinRequest{}
	for rows.Next() {
		r, err := scanJoin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountWaitingJoins is how many requests are unanswered (answered or dismissed ones do not count).
func (s *Store) CountWaitingJoins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM join_requests WHERE decision=''`).Scan(&n)
	return n, err
}

// DropOldestExpiredJoin removes the oldest unanswered request whose window has passed by nowNS, to make room.
func (s *Store) DropOldestExpiredJoin(nowNS int64) error {
	_, err := s.db.Exec(`DELETE FROM join_requests WHERE id=(SELECT id FROM join_requests WHERE decision='' AND expires_ns<=? ORDER BY asked_ns LIMIT 1)`, nowNS)
	return err
}

// AnswerJoin records the person's answer to a waiting request: "allowed" with the worker Allow made, or "denied".
// ErrNotFound: no such waiting request.
func (s *Store) AnswerJoin(id, decision, workerID, workerName string) error {
	r, err := s.db.Exec(`UPDATE join_requests SET decision=?, worker_id=?, worker_name=? WHERE id=? AND decision=''`, decision, workerID, workerName, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// dropUnseenWorker removes remote worker id if no job ever ran on it. Call inside a transaction.
func dropUnseenWorker(tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM workers WHERE id=? AND kind='remote' AND NOT EXISTS (SELECT 1 FROM jobs WHERE worker=?)`, id, id)
	return err
}

// PruneJoins drops the requests whose window has passed by nowNS. An allowed one that nobody picked up takes its
// unseen worker with it; a denied one just goes.
func (s *Store) PruneJoins(nowNS int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT worker_id FROM join_requests WHERE decision='allowed' AND expires_ns<=?`, nowNS)
	if err != nil {
		return err
	}
	var workers []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		workers = append(workers, id)
	}
	rows.Close()
	for _, id := range workers {
		if err := dropUnseenWorker(tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM join_requests WHERE decision<>'' AND expires_ns<=?`, nowNS); err != nil {
		return err
	}
	return tx.Commit()
}

// DismissJoin removes request id from the list. An allowed one nobody picked up takes its unseen worker with it.
// ErrNotFound: no such request.
func (s *Store) DismissJoin(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var decision, worker string
	err = tx.QueryRow(`SELECT decision, worker_id FROM join_requests WHERE id=?`, id).Scan(&decision, &worker)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if decision == "allowed" {
		if err := dropUnseenWorker(tx, worker); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM join_requests WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PickUpJoin hands an allowed request's worker its token, once. The token is new: the one Allow made was never
// given to anyone, so it is replaced. The request is gone after this, so a second pick-up finds nothing.
// ErrNotFound: no such allowed request, or its worker was removed.
func (s *Store) PickUpJoin(id string) (workerID, token string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var decision string
	err = tx.QueryRow(`SELECT decision, worker_id FROM join_requests WHERE id=?`, id).Scan(&decision, &workerID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && decision != "allowed") {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	token = randomText(32)
	r, err := tx.Exec(`UPDATE workers SET token_hash=? WHERE id=? AND kind='remote'`, hashOf(token), workerID)
	if err != nil {
		return "", "", err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return "", "", ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM join_requests WHERE id=?`, id); err != nil {
		return "", "", err
	}
	return workerID, token, tx.Commit()
}
