// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Job is one row of jobs. Text columns that may be NULL read as "".
type Job struct {
	ID         int64
	FileID     int64
	LibraryID  int64
	WorkflowID int64
	Path       string
	State      State
	Top        bool
	QueuedAt   time.Time
	QueuedBy   string
	StartedAt  *time.Time
	FinishedAt *time.Time
	NextTryAt  *time.Time
	WorkerID   string
	Attempt    int
	Tries      int
	SizeBefore *int64
	SizeAfter  *int64
	Outcome    string
	FailedStep string
	Reason     string
	NextAction string
	SkipReason string
	OutputPath string
}

// Attempt is one row of job_attempts.
type Attempt struct {
	JobID      int64
	Number     int
	WorkerID   string
	StartedAt  time.Time
	FinishedAt *time.Time
	Outcome    string
	Failure    string
}

// ListFilter narrows List. Shown is one of the five UI states ("waiting", "running", "done", "failed",
// "skipped"), or "" for all of them. Cancelled jobs are never listed. PathSearch matches a substring of the path,
// ignoring ASCII case. BeforeID pages: only jobs with a smaller id. Limit 0 means 100, and the cap is 500.
type ListFilter struct {
	Shown      string
	LibraryID  int64
	PathSearch string
	BeforeID   int64
	Limit      int
}

const jobColumns = `id, file_id, library_id, workflow_id, path, state, top, queued_at, queued_by,
	started_at, finished_at, next_try_at, COALESCE(worker_id, ''), attempt, tries,
	size_before, size_after, COALESCE(outcome, ''), COALESCE(failed_step, ''), COALESCE(reason, ''),
	COALESCE(next_action, ''), COALESCE(skip_reason, ''), COALESCE(output_path, '')`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (Job, error) {
	var j Job
	var state string
	var top int
	var queuedAt int64
	var started, finished, nextTry, sizeBefore, sizeAfter sql.NullInt64
	err := r.Scan(&j.ID, &j.FileID, &j.LibraryID, &j.WorkflowID, &j.Path, &state, &top, &queuedAt, &j.QueuedBy,
		&started, &finished, &nextTry, &j.WorkerID, &j.Attempt, &j.Tries,
		&sizeBefore, &sizeAfter, &j.Outcome, &j.FailedStep, &j.Reason,
		&j.NextAction, &j.SkipReason, &j.OutputPath)
	if err != nil {
		return Job{}, err
	}
	j.State = State(state)
	j.Top = top != 0
	j.QueuedAt = fromMS(queuedAt)
	j.StartedAt = optTime(started)
	j.FinishedAt = optTime(finished)
	j.NextTryAt = optTime(nextTry)
	j.SizeBefore = optInt(sizeBefore)
	j.SizeAfter = optInt(sizeAfter)
	return j, nil
}

func optTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromMS(n.Int64)
	return &t
}

func optInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// Get returns job id.
func (q *Queue) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(q.db.Read().QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Job{}, ErrNotFound
	}
	return j, err
}

// List returns jobs newest first, by id.
func (q *Queue) List(ctx context.Context, f ListFilter) ([]Job, error) {
	conds, args, err := shownCond(f.Shown)
	if err != nil {
		return nil, err
	}
	if f.LibraryID != 0 {
		conds = append(conds, `library_id = ?`)
		args = append(args, f.LibraryID)
	}
	if f.PathSearch != "" {
		conds = append(conds, `instr(lower(path), lower(?)) > 0`)
		args = append(args, f.PathSearch)
	}
	if f.BeforeID > 0 {
		conds = append(conds, `id < ?`)
		args = append(args, f.BeforeID)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	args = append(args, limit)

	q1 := `SELECT ` + jobColumns + ` FROM jobs WHERE `
	for i, c := range conds {
		if i > 0 {
			q1 += ` AND `
		}
		q1 += c
	}
	q1 += ` ORDER BY id DESC LIMIT ?`

	rows, err := q.db.Read().QueryContext(ctx, q1, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// shownCond is the WHERE for one list: "" is every job, waiting covers retry_wait too. Cancelled jobs are never
// shown. It returns the conditions and their arguments.
func shownCond(shown string) ([]string, []any, error) {
	conds := []string{`state <> 'cancelled'`}
	switch shown {
	case "":
		return conds, nil, nil
	case "waiting":
		return append(conds, `state IN ('waiting', 'retry_wait')`), nil, nil
	case "running", "done", "failed", "skipped":
		return append(conds, `state = ?`), []any{shown}, nil
	}
	return nil, nil, fmt.Errorf("jobs: unknown list state %q", shown)
}

// Counts returns how many jobs are in each inside state. Cancelled jobs are not counted.
func (q *Queue) Counts(ctx context.Context) (map[State]int, error) {
	rows, err := q.db.Read().QueryContext(ctx, `SELECT state, COUNT(*) FROM jobs WHERE state <> 'cancelled' GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[State]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[State(s)] = n
	}
	return out, rows.Err()
}

// Neighbours returns the ids of the list items just above and below job id in the list shown by shown (newest
// first, as List orders it). A missing neighbour is 0. It returns ErrNotFound when job id is not in that list.
func (q *Queue) Neighbours(ctx context.Context, id int64, shown string) (prev, next int64, err error) {
	conds, args, err := shownCond(shown)
	if err != nil {
		return 0, 0, err
	}
	in := append(append([]string{}, conds...), `id = ?`)
	var found int64
	if err := q.db.Read().QueryRowContext(ctx, `SELECT id FROM jobs WHERE `+strings.Join(in, " AND "),
		append(append([]any{}, args...), id)...).Scan(&found); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}
	// Above in the list is a larger id; below is a smaller one.
	above := append(append([]string{}, conds...), `id > ?`)
	below := append(append([]string{}, conds...), `id < ?`)
	if err := q.db.Read().QueryRowContext(ctx, `SELECT COALESCE(MIN(id), 0) FROM jobs WHERE `+strings.Join(above, " AND "),
		append(append([]any{}, args...), id)...).Scan(&prev); err != nil {
		return 0, 0, err
	}
	if err := q.db.Read().QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM jobs WHERE `+strings.Join(below, " AND "),
		append(append([]any{}, args...), id)...).Scan(&next); err != nil {
		return 0, 0, err
	}
	return prev, next, nil
}

// Tally is the counts the shell's Summary and a library row show. Waiting counts retry_wait with
// waiting, as Shown does; cancelled jobs are left out, as in every list.
type Tally struct {
	Waiting int
	Running int
	Done    int
	Failed  int
	Skipped int
	// LastDoneAt is when the latest finished job was done, nil before the first.
	LastDoneAt *time.Time
	// Failed24h counts the jobs that failed in the last 24 hours by the queue's clock.
	Failed24h int
	// SavedBytes is the space the done-and-smaller jobs gave back: the file's size before minus the
	// output's size. Summed over the same jobs, BeforeBytes is the size before.
	SavedBytes  int64
	BeforeBytes int64
}

// Tally counts the jobs of one library, or of every library when libraryID is 0.
func (q *Queue) Tally(ctx context.Context, libraryID int64) (Tally, error) {
	where := `state <> 'cancelled'`
	var args []any
	if libraryID != 0 {
		where += ` AND library_id = ?`
		args = append(args, libraryID)
	}
	var t Tally
	var lastDone sql.NullInt64
	since := q.now().Add(-24 * time.Hour).UnixMilli()
	err := q.db.Read().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state IN ('waiting', 'retry_wait') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'done' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'skipped' THEN 1 ELSE 0 END), 0),
			MAX(CASE WHEN state = 'done' THEN finished_at END),
			COALESCE(SUM(CASE WHEN state = 'failed' AND finished_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'done' AND outcome = 'smaller'
				THEN COALESCE(size_before, 0) - COALESCE(size_after, 0) ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'done' AND outcome = 'smaller'
				THEN COALESCE(size_before, 0) ELSE 0 END), 0)
		FROM jobs WHERE `+where, append([]any{since}, args...)...).
		Scan(&t.Waiting, &t.Running, &t.Done, &t.Failed, &t.Skipped, &lastDone,
			&t.Failed24h, &t.SavedBytes, &t.BeforeBytes)
	if err != nil {
		return Tally{}, fmt.Errorf("count the jobs: %w", err)
	}
	t.LastDoneAt = optTime(lastDone)
	return t, nil
}

// Attempts returns the attempt rows of job id, oldest first.
func (q *Queue) Attempts(ctx context.Context, id int64) ([]Attempt, error) {
	rows, err := q.db.Read().QueryContext(ctx, `
		SELECT job_id, attempt, worker_id, started_at, finished_at, COALESCE(outcome, ''), COALESCE(failure, '')
		FROM job_attempts WHERE job_id = ? ORDER BY attempt`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var started int64
		var finished sql.NullInt64
		if err := rows.Scan(&a.JobID, &a.Number, &a.WorkerID, &started, &finished, &a.Outcome, &a.Failure); err != nil {
			return nil, err
		}
		a.StartedAt = fromMS(started)
		a.FinishedAt = optTime(finished)
		out = append(out, a)
	}
	return out, rows.Err()
}
