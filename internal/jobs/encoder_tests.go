// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "fmt"

// encoderTestRuns is how many test runs per computer are kept (the newest; older runs are pruned).
const encoderTestRuns = 20

// EncoderTest is one encoder's result in one test run of a computer.
type EncoderTest struct {
	Computer string // "server", or worker:<id> for a remote worker
	TestedAt string // RFC 3339, the run's key
	Encoder  string
	Passed   bool
	Detail   string // why it failed, or what else it passed ("10-bit" on hardware)
}

// RecordEncoderTests stores one test run of computer: rows is the run's results, keyed by testedAt. A run
// already stored is left as it is (a worker reports the same run on every heartbeat), and the computer keeps
// only its newest encoderTestRuns runs.
func (s *Store) RecordEncoderTests(computer, testedAt string, rows []EncoderTest) error {
	if computer == "" || testedAt == "" || len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM encoder_tests WHERE computer=? AND tested_at=?`, computer, testedAt).Scan(&seen); err != nil {
		return err
	}
	if seen > 0 {
		return nil
	}
	for _, r := range rows {
		passed := 0
		if r.Passed {
			passed = 1
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO encoder_tests(computer, tested_at, encoder, passed, detail) VALUES(?,?,?,?,?)`,
			computer, testedAt, r.Encoder, passed, r.Detail); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM encoder_tests WHERE computer=? AND tested_at NOT IN (
		SELECT DISTINCT tested_at FROM encoder_tests WHERE computer=? ORDER BY tested_at DESC LIMIT ?)`,
		computer, computer, encoderTestRuns); err != nil {
		return fmt.Errorf("prune encoder tests: %w", err)
	}
	return tx.Commit()
}

// EncoderTests is computer's stored test runs, newest first, each run's results in encoder order.
func (s *Store) EncoderTests(computer string) ([]EncoderTest, error) {
	rows, err := s.db.Query(`SELECT computer, tested_at, encoder, passed, detail FROM encoder_tests
WHERE computer=? ORDER BY tested_at DESC, encoder`, computer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EncoderTest{}
	for rows.Next() {
		var r EncoderTest
		var passed int
		if err := rows.Scan(&r.Computer, &r.TestedAt, &r.Encoder, &passed, &r.Detail); err != nil {
			return nil, err
		}
		r.Passed = passed == 1
		out = append(out, r)
	}
	return out, rows.Err()
}
