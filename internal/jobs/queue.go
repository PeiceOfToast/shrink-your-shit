// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// maxTries is how many failed tries a job gets before it is failed (L-Q-3).
const maxTries = 3

// retryWaits[i] is how long a job waits after its try i+1 failed: 5 minutes, then 30 (L-Q-3).
var retryWaits = []time.Duration{5 * time.Minute, 30 * time.Minute}

// claimRaces bounds how often Claim re-picks after losing a race before it gives up for this call.
const claimRaces = 16

// Queue is the one owner of jobs. Every change goes through a verb here, and every verb emits its History event
// after the commit, never inside the transaction.
type Queue struct {
	db    *store.DB
	bus   *events.Bus
	clock func() time.Time
}

// Options are what a Queue needs. Clock defaults to time.Now.
type Options struct {
	DB    *store.DB
	Bus   *events.Bus
	Clock func() time.Time
}

// New returns the queue over db. Events go to bus.
func New(o Options) *Queue {
	clock := o.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Queue{db: o.DB, bus: o.Bus, clock: clock}
}

func (q *Queue) now() time.Time { return q.clock().UTC() }

// Enqueue puts a file on the queue as waiting. A file that already has an open job gets that job back with
// alreadyOpen true and no new row. A paused or missing library gets ErrLibraryUnavailable and no row.
//
// TODO(L-SCAN-5): refuse a workflow that is not published. The workflows table comes in stage 2.
func (q *Queue) Enqueue(ctx context.Context, fileID, libraryID, workflowID, path, by string) (int64, bool, error) {
	var id int64
	open := false
	now := q.now()
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRow(
			`SELECT id FROM jobs WHERE file_id = ? AND state IN (`+marks(len(openStates))+`)`,
			append([]any{fileID}, stateArgs(openStates)...)...,
		).Scan(&id)
		if err == nil {
			open = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := tx.Exec(`
			INSERT INTO jobs (file_id, library_id, workflow_id, path, state, queued_at, queued_by)
			SELECT ?, ?, ?, ?, 'waiting', ?, ?
			WHERE EXISTS (SELECT 1 FROM libraries WHERE id = ? AND paused = 0)`,
			fileID, libraryID, workflowID, path, ms(now), by, libraryID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLibraryUnavailable
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, false, err
	}
	if !open {
		if err := q.emit(events.KindQueued, events.LevelInfo, id, 0, libraryID, "enqueued", "queued"); err != nil {
			return id, false, err
		}
	}
	return id, open, nil
}

// Order is the queueOrder setting: which waiting job the next claim takes once top jobs are done.
type Order string

const (
	OrderNewest   Order = "newest"
	OrderOldest   Order = "oldest"
	OrderLargest  Order = "largest"
	OrderSmallest Order = "smallest"
)

// ClaimOptions narrow what a claim may take. Except holds job ids to skip. Libraries nil means every library, and
// an empty non-nil slice means none. Order "" means newest.
type ClaimOptions struct {
	Order     Order
	Except    []int64
	Libraries []string
}

// orderBy is the ORDER BY for a claim. top always comes first. Size ordering reads size_before, which nothing
// fills yet, so those orders fall back to queue time until a size source exists.
func (o ClaimOptions) orderBy() (string, error) {
	switch o.Order {
	case "", OrderNewest:
		return "top DESC, queued_at DESC, id DESC", nil
	case OrderOldest:
		return "top DESC, queued_at ASC, id ASC", nil
	case OrderLargest:
		return "top DESC, COALESCE(size_before, 0) DESC, queued_at ASC, id ASC", nil
	case OrderSmallest:
		return "top DESC, COALESCE(size_before, 0) ASC, queued_at ASC, id ASC", nil
	}
	return "", fmt.Errorf("jobs: unknown queue order %q", o.Order)
}

// Claim gives workerID the next job it may run, or returns nil when none is ready. The pick reads the queue; the
// change itself is one guarded move, so when two claimers pick the same job only one wins and the other re-picks.
func (q *Queue) Claim(ctx context.Context, workerID string, opts ClaimOptions) (*Job, error) {
	order, err := opts.orderBy()
	if err != nil {
		return nil, err
	}
	for range claimRaces {
		id, ok, err := q.nextCandidate(ctx, opts, order)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		var attempt int
		now := q.now()
		err = q.db.Tx(ctx, func(tx *sql.Tx) error {
			if err := move(tx, id, verbClaim, Running, change{
				set:  ", started_at = ?, worker_id = ?, attempt = attempt + 1",
				args: []any{ms(now), workerID},
			}); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT attempt FROM jobs WHERE id = ?`, id).Scan(&attempt); err != nil {
				return err
			}
			_, err := tx.Exec(
				`INSERT INTO job_attempts (job_id, attempt, worker_id, started_at) VALUES (?, ?, ?, ?)`,
				id, attempt, workerID, ms(now))
			return err
		})
		var changed *ErrChangedSince
		if errors.As(err, &changed) {
			continue // another claimer took it first; pick again
		}
		if err != nil {
			return nil, err
		}
		job, err := q.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := q.emit(events.KindPicked, events.LevelInfo, id, attempt, job.LibraryID, workerID, "picked"); err != nil {
			return nil, err
		}
		return &job, nil
	}
	return nil, fmt.Errorf("jobs: claim lost %d races in a row", claimRaces)
}

// nextCandidate is the first job a claim may take, read outside the write transaction. A waiting job is always
// eligible; a retry_wait job only once its next_try_at has passed. Paused libraries and except ids are skipped.
func (q *Queue) nextCandidate(ctx context.Context, opts ClaimOptions, order string) (int64, bool, error) {
	conds := []string{
		`(state = 'waiting' OR (state = 'retry_wait' AND next_try_at <= ?))`,
		`library_id IN (SELECT id FROM libraries WHERE paused = 0)`,
	}
	args := []any{ms(q.now())}
	if opts.Libraries != nil {
		if len(opts.Libraries) == 0 {
			return 0, false, nil
		}
		conds = append(conds, `library_id IN (`+marks(len(opts.Libraries))+`)`)
		for _, l := range opts.Libraries {
			args = append(args, l)
		}
	}
	if len(opts.Except) > 0 {
		conds = append(conds, `id NOT IN (`+marks(len(opts.Except))+`)`)
		for _, e := range opts.Except {
			args = append(args, e)
		}
	}
	q1 := `SELECT id FROM jobs WHERE `
	for i, c := range conds {
		if i > 0 {
			q1 += " AND "
		}
		q1 += c
	}
	q1 += " ORDER BY " + order + " LIMIT 1"

	var id int64
	err := q.db.Read().QueryRowContext(ctx, q1, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// Result is what a worker reports when an attempt ends.
type Result struct {
	Verdict Verdict
	// Outcome is set for done: "smaller" or "bigger_kept".
	Outcome string
	// SkipReason is a short code for a skip; Reason is the sentence the person reads.
	SkipReason string
	Reason     string
	FailedStep string
	NextAction string
	SizeBefore int64
	SizeAfter  int64
	OutputPath string
}

// Verdict is how an attempt ended.
type Verdict string

const (
	VerdictDone  Verdict = "done"
	VerdictSkip  Verdict = "skip"
	VerdictRetry Verdict = "retry"
	VerdictFail  Verdict = "fail"
)

// Finish ends attempt of job id with r. A skip is a skip and never a failure (L-Q-9). A retry counts a try and
// waits; the third try used up, or a fail, is failed. The attempt row is written once, and a second Finish on the
// same attempt is refused because the job has already left running.
func (q *Queue) Finish(ctx context.Context, id int64, attempt int, r Result) error {
	if err := r.validate(); err != nil {
		return err
	}
	var libraryID string
	var reason string
	var tries, newTries int
	var to State
	var nextTry *int64
	now := q.now()
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT tries, library_id FROM jobs WHERE id = ?`, id).Scan(&tries, &libraryID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		newTries = tries
		var finishedAt any = ms(now)
		switch r.Verdict {
		case VerdictDone:
			to = Done
		case VerdictSkip:
			to = Skipped
		case VerdictFail:
			to = Failed
		case VerdictRetry:
			newTries = tries + 1
			if newTries < maxTries {
				to = RetryWait
				wait := now.Add(retryWaits[newTries-1])
				nextTry = ptr(ms(wait))
				finishedAt = nil
				reason = fmt.Sprintf("try %d of %d failed; again after %s", newTries, maxTries, retryWaits[newTries-1])
			} else {
				to = Failed
			}
		}
		if err := move(tx, id, verbFinish, to, change{
			set: `, outcome = ?, failed_step = ?, reason = ?, next_action = ?, skip_reason = ?,
				size_before = ?, size_after = ?, output_path = ?, tries = ?, next_try_at = ?,
				finished_at = ?, worker_id = NULL`,
			args: []any{
				nz(r.Outcome), nz(r.FailedStep), nz(r.Reason), nz(r.NextAction), nz(r.SkipReason),
				nzInt(r.SizeBefore), nzInt(r.SizeAfter), nz(r.OutputPath), newTries, nullInt(nextTry), finishedAt,
			},
			cond:     ` AND attempt = ?`,
			condArgs: []any{attempt},
		}); err != nil {
			return err
		}
		res, err := tx.Exec(`
			UPDATE job_attempts SET finished_at = ?, outcome = ?, size_before = ?, size_after = ?, output_path = ?, failure = ?
			WHERE job_id = ? AND attempt = ? AND finished_at IS NULL`,
			ms(now), attemptOutcome(r), nzInt(r.SizeBefore), nzInt(r.SizeAfter), nz(r.OutputPath), nz(r.Reason),
			id, attempt)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("jobs: attempt %d of job %d has no open row", attempt, id)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if to == RetryWait {
		return q.emit(events.KindRetry, events.LevelWarn, id, attempt, libraryID, reason, reason)
	}
	return nil
}

func (r Result) validate() error {
	switch r.Verdict {
	case VerdictDone:
		if r.Outcome != "smaller" && r.Outcome != "bigger_kept" {
			return fmt.Errorf("jobs: done needs outcome smaller or bigger_kept, got %q", r.Outcome)
		}
	case VerdictSkip:
		if r.SkipReason == "" || r.Reason == "" {
			return errors.New("jobs: a skip needs a skip code and a reason")
		}
	case VerdictRetry, VerdictFail:
		if r.Reason == "" {
			return fmt.Errorf("jobs: %s needs a reason", r.Verdict)
		}
	default:
		return fmt.Errorf("jobs: unknown verdict %q", r.Verdict)
	}
	return nil
}

func attemptOutcome(r Result) string {
	if r.Verdict == VerdictDone {
		return r.Outcome
	}
	return string(r.Verdict)
}

// Requeue puts a running job back to waiting. It is not a try (L-Q-5). Startup recovery and a worker that lost its
// lease both use it.
func (q *Queue) Requeue(ctx context.Context, id int64, why string) error {
	var libraryID string
	var attempt int
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT attempt, library_id FROM jobs WHERE id = ?`, id).Scan(&attempt, &libraryID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := move(tx, id, verbRequeue, Waiting, change{set: ", worker_id = NULL, started_at = NULL"}); err != nil {
			return err
		}
		_, err := tx.Exec(`
			UPDATE job_attempts SET finished_at = ?, outcome = 'requeued', failure = ?
			WHERE job_id = ? AND attempt = ? AND finished_at IS NULL`,
			ms(q.now()), nz(why), id, attempt)
		return err
	})
	if err != nil {
		return err
	}
	return q.emit(events.KindRequeued, events.LevelInfo, id, attempt, libraryID, why, why)
}

// CancelResult tells the caller what to stop. WorkerID and Attempt are set when the job was running; the worker
// stops its ffmpeg, the queue does not.
type CancelResult struct {
	WasRunning bool
	WorkerID   string
	Attempt    int
}

// Cancel moves a waiting, retry_wait or running job to cancelled.
func (q *Queue) Cancel(ctx context.Context, id int64) (CancelResult, error) {
	var res CancelResult
	var libraryID string
	var state State
	now := q.now()
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow(
			`SELECT state, COALESCE(worker_id, ''), attempt, library_id FROM jobs WHERE id = ?`, id,
		).Scan(&state, &res.WorkerID, &res.Attempt, &libraryID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := move(tx, id, verbCancel, Cancelled, change{set: ", finished_at = ?", args: []any{ms(now)}}); err != nil {
			return err
		}
		if state != Running {
			return nil
		}
		res.WasRunning = true
		_, err := tx.Exec(`
			UPDATE job_attempts SET finished_at = ?, outcome = 'cancelled'
			WHERE job_id = ? AND attempt = ? AND finished_at IS NULL`,
			ms(now), id, res.Attempt)
		return err
	})
	if err != nil {
		return CancelResult{}, err
	}
	return res, q.emit(events.KindCancelled, events.LevelInfo, id, res.Attempt, libraryID, "cancelled", "cancelled")
}

// Retry puts a failed or cancelled job back to waiting on the same row. Its tries start again at zero, and the
// next claim makes a new attempt row. It is refused while the file has another open job.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	var libraryID string
	var attempt int
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		var fileID string
		if err := tx.QueryRow(
			`SELECT file_id, library_id, attempt FROM jobs WHERE id = ?`, id,
		).Scan(&fileID, &libraryID, &attempt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var other int64
		err := tx.QueryRow(
			`SELECT id FROM jobs WHERE file_id = ? AND id <> ? AND state IN (`+marks(len(openStates))+`) LIMIT 1`,
			append([]any{fileID, id}, stateArgs(openStates)...)...,
		).Scan(&other)
		if err == nil {
			return &ErrFileBusy{JobID: other}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return move(tx, id, verbRetry, Waiting, change{
			set: `, tries = 0, next_try_at = NULL, finished_at = NULL, outcome = NULL,
				failed_step = NULL, reason = NULL, next_action = NULL, skip_reason = NULL`,
		})
	})
	if err != nil {
		return err
	}
	return q.emit(events.KindQueued, events.LevelInfo, id, attempt, libraryID, "retry", "queued again")
}

// Recover runs at startup. Every running job goes back to waiting as a requeue, which is not a try. sweep, when
// not nil, is called for each job first so safety can clean its staging. Jobs another actor has already moved are
// left alone.
func (q *Queue) Recover(ctx context.Context, sweep func(context.Context, Job) error) error {
	rows, err := q.db.Read().QueryContext(ctx, `SELECT id FROM jobs WHERE state = ? ORDER BY id`, string(Running))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if sweep != nil {
			job, err := q.Get(ctx, id)
			if err != nil {
				return err
			}
			if err := sweep(ctx, job); err != nil {
				return err
			}
		}
		err := q.Requeue(ctx, id, "the app restarted while the job was running")
		var changed *ErrChangedSince
		if errors.As(err, &changed) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// emit sends a History event. It runs after the commit that made the change.
func (q *Queue) emit(kind events.Kind, level events.Level, id int64, attempt int, libraryID, reason, message string) error {
	if q.bus == nil {
		return nil
	}
	return q.bus.Emit(events.Event{
		JobID:     id,
		Attempt:   attempt,
		Level:     level,
		Kind:      kind,
		Source:    "jobs",
		LibraryID: libraryID,
		Reason:    reason,
		Message:   message,
	})
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func ptr[T any](v T) *T { return &v }

// nz is a text value, or NULL when empty.
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nzInt is an integer value, or NULL when zero (zero means not measured).
func nzInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
