// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// jobActionInput is POST /jobs/actions: one action for a list of jobs (page 02). Ids are taken as strings or
// numbers, because the page sends strings and a script may send numbers.
type jobActionInput struct {
	Action string            `json:"action"`
	IDs    []json.RawMessage `json:"ids"`
}

// jobActionDone is the answer: the ids that changed, and the ones that did not with the reason. A refused id
// changed nothing.
type jobActionDone struct {
	Done    []string     `json:"done"`
	Refused []jobRefusal `json:"refused"`
}

type jobRefusal struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// jobActions is POST /jobs/actions {action, ids}: Retry, Cancel and Move to the top (pages 02 and 03).
func (s *Server) jobActions(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	rs := s.ready.Load()
	if d == nil || rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	var body jobActionInput
	if !decode(w, r, &body) {
		return
	}
	switch body.Action {
	case "retry", "cancel", "top":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "That is not a job action.", "action", 0)
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Pick the jobs first.", "ids", 0)
		return
	}
	out := jobActionDone{Done: []string{}, Refused: []jobRefusal{}}
	changed := false
	for _, raw := range body.IDs {
		id, text, ok := actionID(raw)
		if !ok {
			out.Refused = append(out.Refused, jobRefusal{ID: text, Code: "job_not_found", Message: "That job doesn't exist."})
			continue
		}
		code, message := s.jobAction(r.Context(), d, rs, body.Action, id)
		if code != "" {
			out.Refused = append(out.Refused, jobRefusal{ID: text, Code: code, Message: message})
			continue
		}
		out.Done = append(out.Done, text)
		changed = true
	}
	// A retry or a move to the top may be first in line now; an idle worker should look at once rather than at
	// its next tick.
	if changed && rs.Wake != nil {
		rs.Wake()
	}
	writeJSON(w, http.StatusOK, out)
}

// actionID reads one id and its text form for the answer. An id that is not a positive whole number is refused
// as a job that doesn't exist, so one bad id does not fail the batch.
func actionID(raw json.RawMessage) (int64, string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, s, err == nil && n > 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil || f != float64(int64(f)) || f <= 0 {
		return 0, string(raw), false
	}
	return int64(f), strconv.FormatInt(int64(f), 10), true
}

// jobAction runs one action for one job. It returns "" when the job changed, or the refusal's code and message.
func (s *Server) jobAction(ctx context.Context, d *jobDeps, rs *Deps, action string, id int64) (code, message string) {
	j, err := d.queue.Get(ctx, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return "job_not_found", "That job doesn't exist."
	}
	if err != nil {
		return "internal", "Something went wrong. Try again."
	}
	switch action {
	case "retry":
		return s.retryJob(ctx, d, j)
	case "cancel":
		return s.cancelJob(ctx, d, rs, j)
	}
	return s.topJob(ctx, d, j)
}

// retryJob puts a failed or cancelled job back to waiting on the same row.
func (s *Server) retryJob(ctx context.Context, d *jobDeps, j jobs.Job) (code, message string) {
	switch j.State {
	case jobs.Failed, jobs.Cancelled:
	case jobs.Running:
		return "not_retryable", "This job is running now."
	case jobs.Done, jobs.Skipped:
		// A finished job has "Run again", which brings in the new file as the original (page 03): Retry
		// would run the file again as if it had failed.
		return "not_retryable", "This job has already finished. Use Run again."
	default:
		return "not_retryable", "This job has not finished yet."
	}
	if msg, refused := jobs.RetryRefused(j.FailureCode); refused {
		return "not_retryable", msg
	}
	err := d.queue.Retry(ctx, j.ID)
	var notRetryable *jobs.ErrNotRetryable
	var busy *jobs.ErrFileBusy
	var changed *jobs.ErrChangedSince
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &notRetryable):
		return "not_retryable", notRetryable.Message
	case errors.As(err, &busy):
		return "file_busy", "This file already has a job waiting or running."
	case errors.As(err, &changed):
		// Another actor moved the job between the read and the retry: the state the person saw is gone.
		return "not_retryable", "The job changed while this was asked. Try again."
	case errors.Is(err, jobs.ErrNotFound):
		return "job_not_found", "That job doesn't exist."
	}
	return "internal", "Something went wrong. Try again."
}

// cancelJob ends a waiting or running job as cancelled. A running job is marked first and then stopped, so the
// row ends cancelled even if the worker is slow to notice (L-Q-7).
func (s *Server) cancelJob(ctx context.Context, d *jobDeps, rs *Deps, j jobs.Job) (code, message string) {
	switch j.State {
	case jobs.Waiting, jobs.RetryWait, jobs.Running:
	default:
		return "not_cancellable", "This job has already finished."
	}
	res, err := d.queue.Cancel(ctx, j.ID)
	var changed *jobs.ErrChangedSince
	switch {
	case err == nil:
	case errors.As(err, &changed):
		return "not_cancellable", "This job has already finished."
	case errors.Is(err, jobs.ErrNotFound):
		return "job_not_found", "That job doesn't exist."
	default:
		return "internal", "Something went wrong. Try again."
	}
	if res.WasRunning && rs.StopJob != nil {
		// The worker kills ffmpeg and removes its staging; the row is already cancelled, so the run's own
		// cancel then finds nothing to do.
		rs.StopJob(j.ID)
	}
	return "", ""
}

// topJob moves a waiting job first in the claim order. A job that is not waiting is refused as not_movable,
// not not_retryable: the page reads the code, and Retry has its own refusals (ui-shop, 11-28).
func (s *Server) topJob(ctx context.Context, d *jobDeps, j jobs.Job) (code, message string) {
	if j.State != jobs.Waiting && j.State != jobs.RetryWait {
		return "not_movable", "Only a waiting job can be moved to the top."
	}
	err := d.queue.Top(ctx, j.ID)
	var changed *jobs.ErrChangedSince
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &changed):
		return "not_movable", "Only a waiting job can be moved to the top."
	case errors.Is(err, jobs.ErrNotFound):
		return "job_not_found", "That job doesn't exist."
	}
	return "internal", "Something went wrong. Try again."
}
