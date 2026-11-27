// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Builtin is the in-process Source: a thin adapter over the jobs queue. It builds each job's flow input from the
// library, the file as the scan recorded it, and the workflow's published revision.
type Builtin struct {
	Queue *jobs.Queue
	DB    *store.DB
	Flows *flow.Store
}

// Claim takes the next job, or returns nil when none is ready.
func (b Builtin) Claim(ctx context.Context, workerID string) (*Work, error) {
	j, err := b.Queue.Claim(ctx, workerID, jobs.ClaimOptions{})
	if err != nil || j == nil {
		return nil, err
	}
	in, err := b.input(ctx, *j, workerID)
	var tr *transientError
	return &Work{Job: *j, In: in, Err: err, Transient: errors.As(err, &tr)}, nil
}

// transientError marks a database read in input that failed for now. The job retries instead of failing.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func (b Builtin) input(ctx context.Context, j jobs.Job, workerID string) (flow.RunInput, error) {
	var root, rel, fileID string
	var size, mtime int64
	err := b.DB.Read().QueryRowContext(ctx, `
		SELECT l.path, f.rel_path, f.size, f.mtime, f.file_id
		FROM files f JOIN libraries l ON l.id = f.library_id
		WHERE f.id = ?`, j.FileID).Scan(&root, &rel, &size, &mtime, &fileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return flow.RunInput{}, errors.New("the file is no longer in its library")
		}
		return flow.RunInput{}, &transientError{err}
	}
	wf, err := b.Flows.Get(ctx, j.WorkflowID)
	if errors.Is(err, flow.ErrNotFound) {
		return flow.RunInput{}, errors.New("the workflow is no longer there")
	}
	if err != nil {
		return flow.RunInput{}, &transientError{err}
	}
	if wf.CurrentRev == 0 {
		return flow.RunInput{}, errors.New("the workflow has no published revision")
	}
	return flow.RunInput{
		JobID: j.ID, Attempt: j.Attempt, LibraryID: strconv.FormatInt(j.LibraryID, 10), WorkerID: workerID,
		WorkflowID: j.WorkflowID, Revision: wf.CurrentRev,
		LibraryRoot: root, Source: filepath.Join(root, rel),
		Size: size, MTime: time.Unix(0, mtime).UTC(), FileID: fileID,
	}, nil
}

// Finish writes a job's result.
func (b Builtin) Finish(ctx context.Context, id int64, attempt int, r jobs.Result) error {
	return b.Queue.Finish(ctx, id, attempt, r)
}

// Requeue puts a running job back to waiting. It is not a try.
func (b Builtin) Requeue(ctx context.Context, id int64, why string) error {
	return b.Queue.Requeue(ctx, id, why)
}

// Cancel cancels a job.
func (b Builtin) Cancel(ctx context.Context, id int64) error {
	_, err := b.Queue.Cancel(ctx, id)
	return err
}
