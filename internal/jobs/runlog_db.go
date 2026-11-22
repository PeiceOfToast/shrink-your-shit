// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// User review 11-13 item E: a job's log lines, steps and failure summary live in SQLite (migration 39), so the
// Jobs list, the failure summary and the log view read the database, not the files. The run log file keeps the
// full raw output.

// maxKeyLines is how many ffmpeg error lines the database keeps for a failure (the API shows up to 5).
const maxKeyLines = 20

// Log levels: what a line is for the reader (the drawer and the download show them).
const (
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
)

// maxLogText is the longest text a log line keeps, in characters; a longer one is cut.
const maxLogText = 2000

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// base64Run matches a run long enough to be a blob rather than a word (R13): 200 or more base64 characters
// in a row. Punctuation that words and paths have (dots, dashes, underscores, colons, spaces) breaks a run,
// so a path or a command line is left as it is.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/]{200,}={0,2}`)

// CleanLogText makes text one plain line for the log: no ANSI colour codes, no line breaks, no base64 blobs
// and at most 2,000 characters. A blob becomes "… (base64 data, N chars)"; a longer text is cut and ends with
// "… (cut, N chars)", N being its full length.
func CleanLogText(text string) string {
	text = ansiSeq.ReplaceAllString(text, "")
	text = base64Run.ReplaceAllStringFunc(text, func(s string) string {
		return fmt.Sprintf("… (base64 data, %d chars)", len(s))
	})
	text = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text)
	runes := []rune(text)
	if len(runes) <= maxLogText {
		return text
	}
	suffix := fmt.Sprintf("… (cut, %d chars)", len(runes))
	return string(runes[:maxLogText-utf8.RuneCountInString(suffix)]) + suffix
}

// LogLine is one line of a job's log: its step (the index in RunSteps), its section (start, input, encode, output,
// replace, summary; "" for lines written before sections), its level, a stream (app, stdout, stderr) and its text.
type LogLine struct {
	Seq     int64
	At      string
	Step    int
	Section string
	Level   string
	Stream  string
	Text    string
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
	// F7: the encode step's speed, 0 when it is not known (an older row, a copy, a failure).
	Encoder string  // the encoder the command names: hevc_qsv, libx265, ...
	Speed   float64 // the realtime multiple: the media's length over the encode's seconds
	FPS     float64 // average frames encoded per second
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

// logTimeFormat is a log line's time: UTC to the millisecond, so lines written in the same second keep their order
// and the download can show HH:MM:SS.mmm (log-spec section 2). Lines written before this have whole seconds.
const logTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// AppendLogLine adds a line to job id's log, in order, at step (its index in RunSteps; 0 before any step), in
// section, at level (info unless warning or error). The text is cleaned by CleanLogText.
func (s *Store) AppendLogLine(id int64, step int, section, level, stream, text string) error {
	if level != LevelWarning && level != LevelError {
		level = LevelInfo
	}
	_, err := s.db.Exec(`INSERT INTO job_log_lines(job_id, seq, ts, step, section, level, stream, text)
SELECT ?, COALESCE(MAX(seq),0)+1, ?, ?, ?, ?, ?, ? FROM job_log_lines WHERE job_id=?`, id, s.now().UTC().Format(logTimeFormat), step, section, level, stream, CleanLogText(text), id)
	return err
}

// JobRevisions are the workflow revision job id ran with and the one it was queued under (0: none
// recorded; foundation slice 9).
func (s *Store) JobRevisions(id int64) (ran, queued int64, err error) {
	err = s.db.QueryRow(`SELECT workflow_rev, queued_rev FROM jobs WHERE id=?`, id).Scan(&ran, &queued)
	return ran, queued, err
}

// JobWorkflowRev is the workflow revision job id ran with (0: none recorded).
func (s *Store) JobWorkflowRev(id int64) (int64, error) {
	var rev int64
	err := s.db.QueryRow(`SELECT workflow_rev FROM jobs WHERE id=?`, id).Scan(&rev)
	return rev, err
}

// logLineCols selects a log line's columns, in LogLine's order.
const logLineCols = `SELECT seq, ts, step, section, level, stream, text FROM job_log_lines`

// LogLines is job id's log lines, oldest first.
func (s *Store) LogLines(id int64) ([]LogLine, error) {
	rows, err := s.db.Query(logLineCols+` WHERE job_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.At, &l.Step, &l.Section, &l.Level, &l.Stream, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RecordRunStep stores step idx of job id (replacing an earlier record of the same step).
func (s *Store) RecordRunStep(id int64, st RunStep) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO job_run_steps(job_id, idx, name, started_at, took_ms, outcome, command, probe,
  check_score, check_min, check_passed, output_lines, result, encoder, speed, fps) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, st.Index, st.Name, st.StartedAt, st.TookMs, st.Outcome, st.Command, st.Probe,
		st.CheckScore, st.CheckMin, st.CheckPassed, st.OutputLines, st.Result, st.Encoder, st.Speed, st.FPS)
	return err
}

// RunSteps is job id's steps, in order.
func (s *Store) RunSteps(id int64) ([]RunStep, error) {
	rows, err := s.db.Query(`SELECT idx, name, started_at, took_ms, outcome, command, probe, check_score, check_min, check_passed, output_lines, result,
  encoder, speed, fps
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
		if err := rows.Scan(&st.Index, &st.Name, &st.StartedAt, &st.TookMs, &st.Outcome, &st.Command, &st.Probe, &score, &min, &passed, &st.OutputLines, &st.Result,
			&st.Encoder, &st.Speed, &st.FPS); err != nil {
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

// RecordJobStats records job id's run counters (replacing an earlier record): the ffmpeg runs it needed and
// the bytes it wrote (temp files and the final one).
func (s *Store) RecordJobStats(id int64, passes int, bytesWritten int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO job_run_stats(job_id, passes, bytes_written) VALUES(?,?,?)`, id, passes, bytesWritten)
	return err
}

// JobStats is job id's run counters; ok false when the job recorded none (a job from before L2, or one run on
// another computer), so the summary reads them as null.
func (s *Store) JobStats(id int64) (passes int, bytesWritten int64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT passes, bytes_written FROM job_run_stats WHERE job_id=?`, id).Scan(&passes, &bytesWritten)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return passes, bytesWritten, true, nil
}

// Move is one file action the replace step took (design's moves[]): what it did, to which file, and whether
// it worked (error is its words when it did not).
type Move struct {
	Action string // move | copy | chmod | chown | remove | write_sidecar
	From   string
	To     string
	OK     bool
	Error  string
}

// RecordJobMove adds one file action to job id's replace step, after those recorded before it (log-spec R14).
func (s *Store) RecordJobMove(id int64, m Move) error {
	ok := 0
	if m.OK {
		ok = 1
	}
	_, err := s.db.Exec(`INSERT INTO job_run_moves(job_id, seq, action, from_path, to_path, ok, error)
SELECT ?, COALESCE(MAX(seq),0)+1, ?, ?, ?, ?, ? FROM job_run_moves WHERE job_id=?`,
		id, m.Action, m.From, m.To, ok, CleanLogText(m.Error), id)
	return err
}

// JobMoves is job id's file actions, in the order the replace step took them (none for a job that recorded
// none: one from before this, or one that never reached the replace).
func (s *Store) JobMoves(id int64) ([]Move, error) {
	rows, err := s.db.Query(`SELECT action, from_path, to_path, ok, error FROM job_run_moves WHERE job_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Move{}
	for rows.Next() {
		var m Move
		var ok int
		if err := rows.Scan(&m.Action, &m.From, &m.To, &ok, &m.Error); err != nil {
			return nil, err
		}
		m.OK = ok != 0
		out = append(out, m)
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

// ClearFailureSummary removes job id's failure summary: a new attempt has started, and an earlier attempt's
// failure is not that attempt's own.
func (s *Store) ClearFailureSummary(id int64) error {
	_, err := s.db.Exec(`DELETE FROM job_failures WHERE job_id=?`, id)
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
SELECT ?, COALESCE(MAX(seq),0)+1, ?, 0, 'app', ? FROM job_log_lines WHERE job_id=?`, id, s.stamp(), CleanLogText(line), id); err != nil {
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
	rows, err := s.db.Query(logLineCols+` WHERE job_id=? AND seq>? ORDER BY seq LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.Seq, &l.At, &l.Step, &l.Section, &l.Level, &l.Stream, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RemoveJobLogs deletes the log lines and steps of jobs that are gone (their rows were removed).
func (s *Store) RemoveJobLogs(ids []int64) error {
	for _, id := range ids {
		for _, q := range []string{`DELETE FROM job_log_lines WHERE job_id=?`, `DELETE FROM job_run_steps WHERE job_id=?`, `DELETE FROM job_failures WHERE job_id=?`, `DELETE FROM job_run_stats WHERE job_id=?`, `DELETE FROM job_run_moves WHERE job_id=?`} {
			if _, err := s.db.Exec(q, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// PruneJobLogs deletes the log lines and run steps of finished jobs (done, failed, kept, skipped) whose last change is
// before the cutoff (logKeepDays). The job rows and their failure summaries stay; their logState then says expired.
// It returns how many log lines went. SQLite reuses the freed pages; the file shrinks only when it is vacuumed (a later step).
func (s *Store) PruneJobLogs(before time.Time) (int64, error) {
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE status IN ('done','failed','kept','skipped') AND updated<?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var n int64
	for _, id := range ids {
		r, err := s.db.Exec(`DELETE FROM job_log_lines WHERE job_id=?`, id)
		if err != nil {
			return n, err
		}
		k, _ := r.RowsAffected()
		n += k
		// the run counters stay: they are the job's summary, not its log
		if _, err := s.db.Exec(`DELETE FROM job_run_steps WHERE job_id=?`, id); err != nil {
			return n, err
		}
		if _, err := s.db.Exec(`DELETE FROM job_run_moves WHERE job_id=?`, id); err != nil {
			return n, err
		}
	}
	return n, nil
}
