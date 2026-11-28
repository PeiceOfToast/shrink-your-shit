// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// convertAgain is POST /jobs/{id}/convert-again (S3-6b, Run again): a done or skipped job runs again on its own row,
// converting the file at its path as it is now. The answer is the updated job, so the panel stays open on the new
// attempt. Any other state is refused with a plain sentence.
func (s *Server) convertAgain(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	rs := s.ready.Load()
	if d == nil || rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, ok := parseJobID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	err := d.queue.RunAgain(r.Context(), id)
	var notRunnable *jobs.ErrNotRunnable
	var busy *jobs.ErrFileBusy
	switch {
	case err == nil:
	case errors.Is(err, jobs.ErrNotFound):
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	case errors.As(err, &notRunnable):
		writeError(w, http.StatusConflict, "not_runnable", runAgainRefusal(notRunnable.State), "", 0)
		return
	case errors.As(err, &busy):
		writeError(w, http.StatusConflict, "file_busy", "This file already has a job waiting or running.", "", 0)
		return
	default:
		internalError(w)
		return
	}
	if rs.Wake != nil {
		rs.Wake()
	}
	j, err := d.queue.Get(r.Context(), id)
	if err != nil {
		internalError(w)
		return
	}
	detail, err := s.detail(r.Context(), d, j)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// runAgainRefusal is the plain sentence for a job that is not finished, by its state.
func runAgainRefusal(state jobs.State) string {
	switch state {
	case jobs.Failed, jobs.Cancelled:
		return "This job did not finish. Use Retry."
	case jobs.Running:
		return "This job is running now."
	}
	return "This job has not finished yet."
}
