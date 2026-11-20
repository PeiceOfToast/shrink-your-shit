// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "fmt"

// EncryptSecret seals a secret (a notification URL) the way share passwords are sealed.
func EncryptSecret(plaintext string) (ct, nonce []byte, err error) {
	return encryptSharePassword(plaintext)
}

// DecryptSecret opens what EncryptSecret sealed.
func DecryptSecret(ct, nonce []byte) (string, error) { return decryptSharePassword(ct, nonce) }

// LatestEventSeq is the newest event's seq (0: none yet). The notifier starts from here, so older history is
// not sent again.
func (s *Store) LatestEventSeq() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&n)
	return n, err
}

// Fallback is one graphics-encoder fall-back as its step row records it.
type Fallback struct {
	Row   int64 // the job_steps row id
	JobID int64
	Words string
}

// FallbacksAfter are the encoder fall-backs recorded after row, oldest first.
func (s *Store) FallbacksAfter(row int64, limit int) ([]Fallback, error) {
	rows, err := s.db.Query(`SELECT rowid, job_id, words FROM job_steps WHERE step=? AND rowid>? ORDER BY rowid LIMIT ?`, EncoderFallbackStep, row, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fallback
	for rows.Next() {
		var f Fallback
		if err := rows.Scan(&f.Row, &f.JobID, &f.Words); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// LatestStepRow is the newest job_steps row id (0: none yet).
func (s *Store) LatestStepRow() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM job_steps`).Scan(&n)
	return n, err
}

// Batch is what a batch summary reports: the work finished since a time, and what is still waiting.
type Batch struct {
	Converted int64 // done since the time (a restored file is not converted)
	Failed    int64 // failed since the time
	Saved     int64 // bytes smaller, for the files done since the time
	Waiting   int64 // converted files whose original is still there to be freed (now, not since)
	Active    int64 // queued or running now
}

// BatchSince reports the batch since the given time (RFC3339, UTC).
func (s *Store) BatchSince(since string) (Batch, error) {
	var b Batch
	err := s.db.QueryRow(`SELECT
  COALESCE(SUM(CASE WHEN `+convertedNow("jobs")+` AND updated>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN status='failed' AND updated>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN `+convertedNow("jobs")+` AND updated>=? THEN size_before-size_after ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN status='done' AND `+leftSQL+` THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN status IN ('queued','running') THEN 1 ELSE 0 END),0)
FROM jobs`, since, since, since).Scan(&b.Converted, &b.Failed, &b.Saved, &b.Waiting, &b.Active)
	if err != nil {
		return b, fmt.Errorf("batch: %w", err)
	}
	return b, nil
}
