// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// User review 11-13 item E: a job's log lines, steps and failure summary live in SQLite (migration 39), so the
// Jobs list, the failure summary and the log view read the database, not the files. The run log file keeps the
// full raw output.

// maxKeyLines is how many ffmpeg error lines the database keeps for a failure (the API shows up to 5).
const maxKeyLines = 20

// LogLine is one line of a job's log: its step (the index in RunSteps), a stream (app, stdout, stderr) and its text.
type LogLine struct {
	Seq    int64
	At     string
	Step   int
	Stream string
	Text   string
}

// RunStep is one step of a job's run: when it started, how long it took, what it ran and what came of it.
type RunStep struct {
	Index       int
	Name        string
	StartedAt   string
	TookMs      int64
	Outcome     string
	Command     string   // the exact ffmpeg or ffprobe command line, "" when none
	Probe       string   // the probe summary as JSON, "" when none
	CheckScore  *float64 // the picture check's SSIM, nil when there was no check
	CheckMin    *float64
	CheckPassed *bool
	OutputLines int // the ffmpeg lines this step wrote to the log
	Result      string
}

// FailureSummary says why a job failed: the step, when, the ffmpeg exit code and the error lines that matter.
type FailureSummary struct {
	Step, StepCount int
	AfterMs         int64
	MediaTime, Hint string
	ExitCode        int  // -1: none (ffmpeg didn't run, or was killed by a signal)
	FfmpegRan       bool // false: ffmpeg never started (its program could not be run)
	KeyLines        []string
}

// AppendLogLine adds a line to job id's log, in order, at step (its index in RunSteps; 0 before any step).
func (s *Store) AppendLogLine(id int64, step int, stream, text string) error {
	_, err := s.db.Exec(`INSERT INTO job_log_lines(job_id, seq, ts, step, stream, text)
SELECT ?, COALESCE(MAX(seq),0)+1, ?, ?, ?, ? FROM job_log_lines WHERE job_id=?`, id, s.stamp(), step, stream, text, id)
	return err
}

// LogLines is job id's log lines, oldest first.
func (s *Store) LogLines(id int64) ([]LogLine, error) {
	rows, err := s.db.Query(`SELECT seq, ts, step, stream, text FROM job_log_lines WHERE job_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.At, &l.Step, &l.Stream, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RecordRunStep stores step idx of job id (replacing an earlier record of the same step).
func (s *Store) RecordRunStep(id int64, st RunStep) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO job_run_steps(job_id, idx, name, started_at, took_ms, outcome, command, probe,
  check_score, check_min, check_passed, output_lines, result) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, st.Index, st.Name, st.StartedAt, st.TookMs, st.Outcome, st.Command, st.Probe,
		st.CheckScore, st.CheckMin, st.CheckPassed, st.OutputLines, st.Result)
	return err
}

// RunSteps is job id's steps, in order.
func (s *Store) RunSteps(id int64) ([]RunStep, error) {
	rows, err := s.db.Query(`SELECT idx, name, started_at, took_ms, outcome, command, probe, check_score, check_min, check_passed, output_lines, result
FROM job_run_steps WHERE job_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunStep{}
	for rows.Next() {
		var st RunStep
		var score, min sql.NullFloat64
		var passed sql.NullBool
		if err := rows.Scan(&st.Index, &st.Name, &st.StartedAt, &st.TookMs, &st.Outcome, &st.Command, &st.Probe, &score, &min, &passed, &st.OutputLines, &st.Result); err != nil {
			return nil, err
		}
		if score.Valid {
			st.CheckScore = &score.Float64
		}
		if min.Valid {
			st.CheckMin = &min.Float64
		}
		if passed.Valid {
			st.CheckPassed = &passed.Bool
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SetFailureSummary records why job id failed (replacing an earlier one). At most 20 key lines are kept.
func (s *Store) SetFailureSummary(id int64, f FailureSummary) error {
	lines := f.KeyLines
	if len(lines) > maxKeyLines {
		lines = lines[len(lines)-maxKeyLines:] // the last ones are the ones that show where it stopped
	}
	if lines == nil {
		lines = []string{}
	}
	b, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO job_failures(job_id, step, step_count, after_ms, media_time, hint, exit_code, ffmpeg_ran, key_lines)
VALUES(?,?,?,?,?,?,?,?,?)`, id, f.Step, f.StepCount, f.AfterMs, f.MediaTime, f.Hint, f.ExitCode, f.FfmpegRan, string(b))
	return err
}

// FailureOf is job id's failure summary; nil when it has none (not failed, or failed before this was recorded).
func (s *Store) FailureOf(id int64) (*FailureSummary, error) {
	var f FailureSummary
	var raw string
	err := s.db.QueryRow(`SELECT step, step_count, after_ms, media_time, hint, exit_code, ffmpeg_ran, key_lines FROM job_failures WHERE job_id=?`, id).
		Scan(&f.Step, &f.StepCount, &f.AfterMs, &f.MediaTime, &f.Hint, &f.ExitCode, &f.FfmpegRan, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &f.KeyLines); err != nil {
		return nil, err
	}
	return &f, nil
}

// LogLineCount is how many log lines job id has in the database (0: none, a job before this was recorded).
func (s *Store) LogLineCount(id int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM job_log_lines WHERE job_id=?`, id).Scan(&n)
	return n, err
}

// ImportRunLogs moves the old run-log files (dir/<job id>.log, from before the log lived in SQLite) into the
// database as app lines, then removes each file once its lines are committed. Once the folder is empty this does
// nothing, so it is safe to run at every start. It returns how many files it imported.
func (s *Store) ImportRunLogs(dir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, f := range files {
		id, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(f), ".log"), 10, 64)
		if err != nil || id <= 0 {
			continue // not a job's log
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return imported, err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return imported, err
		}
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if _, err := tx.Exec(`INSERT INTO job_log_lines(job_id, seq, ts, step, stream, text)
SELECT ?, COALESCE(MAX(seq),0)+1, ?, 0, 'app', ? FROM job_log_lines WHERE job_id=?`, id, s.stamp(), line, id); err != nil {
				tx.Rollback()
				return imported, err
			}
		}
		if err := tx.Commit(); err != nil {
			return imported, err
		}
		if err := os.Remove(f); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// LogLinesAfter is job id's log lines after sequence number after, at most limit of them: a reader's next piece.
func (s *Store) LogLinesAfter(id, after int64, limit int) ([]LogLine, error) {
	rows, err := s.db.Query(`SELECT seq, ts, step, stream, text FROM job_log_lines WHERE job_id=? AND seq>? ORDER BY seq LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.At, &l.Step, &l.Stream, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RemoveJobLogs deletes the log lines and steps of jobs that are gone (their rows were removed).
func (s *Store) RemoveJobLogs(ids []int64) error {
	for _, id := range ids {
		for _, q := range []string{`DELETE FROM job_log_lines WHERE job_id=?`, `DELETE FROM job_run_steps WHERE job_id=?`, `DELETE FROM job_failures WHERE job_id=?`} {
			if _, err := s.db.Exec(q, id); err != nil {
				return err
			}
		}
	}
	return nil
}
