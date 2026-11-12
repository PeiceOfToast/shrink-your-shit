// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Remote workers (V7b, slices/v2-engine/v7-plan.md): a worker on another computer pairs with a
// one-time code the person makes on the Workers page, and gets its own long-lived token. Only the
// token's hash is kept; removing the worker revokes it.

// PairingFor is how long a pairing code works.
const PairingFor = 15 * time.Minute

// ErrPairingInvalid: the code is unknown, used already, or expired.
var ErrPairingInvalid = errors.New("this pairing code is not valid: make a new one on the Workers page")

// RemoteWorker is a paired worker on another computer.
type RemoteWorker struct {
	ID, Name, Version string
	APILevel          int
	LastSeen          string // when it last called (RFC 3339; "" never)
	Capabilities      string // JSON, as it reported them
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func randomText(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewPairing makes a one-time pairing code (8 letters and digits, shown as XXXX-XXXX), valid for
// PairingFor.
func (s *Store) NewPairing(now time.Time) (code string, expires time.Time, err error) {
	b := make([]byte, 5)
	rand.Read(b)
	raw := base32.StdEncoding.EncodeToString(b) // 8 characters
	code = raw[:4] + "-" + raw[4:]
	expires = now.Add(PairingFor).UTC()
	_, err = s.db.Exec(`INSERT INTO worker_pairings(code_hash, expires) VALUES (?,?)`, hashOf(normalCode(code)), expires.Format(time.RFC3339))
	return code, expires, err
}

// normalCode is a typed code as stored: upper case, without the dash or spaces.
func normalCode(c string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(c)))
}

// Pair exchanges a pairing code for a new remote worker and its token (shown once, to the worker).
func (s *Store) Pair(code, name, version string, apiLevel int, capabilities string, now time.Time) (RemoteWorker, string, error) {
	was := s.Paused() // a paired worker starts on: with every other off, that is a resume (item 2)
	tx, err := s.db.Begin()
	if err != nil {
		return RemoteWorker{}, "", err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE worker_pairings SET used_at=? WHERE code_hash=? AND used_at='' AND expires>?`,
		now.UTC().Format(time.RFC3339), hashOf(normalCode(code)), now.UTC().Format(time.RFC3339))
	if err != nil {
		return RemoteWorker{}, "", err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return RemoteWorker{}, "", ErrPairingInvalid
	}
	if strings.TrimSpace(name) == "" {
		name = "Another computer"
	}
	if capabilities == "" {
		capabilities = "{}"
	}
	w := RemoteWorker{ID: "w-" + strings.ToLower(randomText(6)), Name: name, Version: version, APILevel: apiLevel,
		LastSeen: now.UTC().Format(time.RFC3339), Capabilities: capabilities}
	token := randomText(32)
	if _, err := tx.Exec(`INSERT INTO workers(id, name, kind, token_hash, version, api_level, last_seen, capabilities) VALUES (?,?,?,?,?,?,?,?)`,
		w.ID, w.Name, "remote", hashOf(token), w.Version, w.APILevel, w.LastSeen, w.Capabilities); err != nil {
		return RemoteWorker{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return RemoteWorker{}, "", err
	}
	return w, token, s.pausedChanged(was)
}

// WorkerByToken is the remote worker whose token this is (ErrNotFound: none, or it was removed).
func (s *Store) WorkerByToken(token string) (RemoteWorker, error) {
	if token == "" {
		return RemoteWorker{}, ErrNotFound
	}
	var w RemoteWorker
	err := s.db.QueryRow(`SELECT id, name, version, api_level, last_seen, capabilities FROM workers WHERE kind='remote' AND token_hash=?`, hashOf(token)).
		Scan(&w.ID, &w.Name, &w.Version, &w.APILevel, &w.LastSeen, &w.Capabilities)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// SeenWorker records that remote worker id called now, with its version.
func (s *Store) SeenWorker(id, version string, apiLevel int, now time.Time) error {
	_, err := s.db.Exec(`UPDATE workers SET last_seen=?, version=?, api_level=? WHERE id=? AND kind='remote'`, now.UTC().Format(time.RFC3339), version, apiLevel, id)
	return err
}

// RemoteWorkers lists the paired workers, oldest first.
func (s *Store) RemoteWorkers() ([]RemoteWorker, error) {
	rows, err := s.db.Query(`SELECT id, name, version, api_level, last_seen, capabilities FROM workers WHERE kind='remote' ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RemoteWorker{}
	for rows.Next() {
		var w RemoteWorker
		if err := rows.Scan(&w.ID, &w.Name, &w.Version, &w.APILevel, &w.LastSeen, &w.Capabilities); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RemoveWorker removes remote worker id: its token stops working at once, and the jobs it was running
// go back to the queue (their leases end with them).
func (s *Store) RemoveWorker(id string) error {
	was := s.Paused()
	defer s.pausedChanged(was)
	r, err := s.db.Exec(`DELETE FROM workers WHERE id=? AND kind='remote'`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	running, err := jobIDs(s.db, `SELECT id FROM jobs WHERE status=? AND worker=?`, Running, id)
	if err != nil {
		return err
	}
	for _, j := range running {
		s.Release(j)
	}
	return nil
}

// LeaseHeld is the running job that lease holds for worker, with when the lease runs out (false: the
// lease ended: expired, given back, or never this worker's).
func (s *Store) LeaseHeld(lease, worker string) (Job, string, bool) {
	if lease == "" {
		return Job{}, "", false
	}
	var id int64
	var expires string
	err := s.db.QueryRow(`SELECT id, lease_expires FROM jobs WHERE lease_id=? AND worker=? AND status=?`, lease, worker, Running).Scan(&id, &expires)
	if err != nil {
		return Job{}, "", false
	}
	j, err := s.Get(id)
	return j, expires, err == nil
}
