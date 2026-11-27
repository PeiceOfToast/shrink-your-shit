// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// maxJobLog bounds one job's log read. A job's events are few; the cap keeps a runaway job from filling memory.
const maxJobLog = 20000

// logEventJSON is one line of the job log as types.ts shapes LogEvent.
type logEventJSON struct {
	Seq        int64   `json:"seq"`
	TS         string  `json:"ts"`
	Level      string  `json:"level"`
	Kind       string  `json:"kind"`
	Source     string  `json:"source"`
	Message    string  `json:"message"`
	JobID      *string `json:"jobId"`
	LibraryID  *string `json:"libraryId"`
	WorkerID   *string `json:"workerId"`
	NextAction *string `json:"nextAction,omitempty"`
}

// historyJSON is one line of the Summary's history (types.ts HistoryLine).
type historyJSON struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// historyKinds are the lifecycle lines a job's history shows, in the order they happen.
var historyKinds = map[events.Kind]bool{
	events.KindQueued: true, events.KindPicked: true, events.KindRetry: true, events.KindRequeued: true,
	events.KindCancelled: true, events.KindRunStart: true, events.KindRunEnd: true,
	events.KindWarning: true, events.KindError: true,
}

// jobEvents reads every event of a job, oldest first, one page at a time.
func (s *Server) jobEvents(ctx context.Context, d *jobDeps, id int64) ([]events.Event, error) {
	var all []events.Event
	page := events.Page{Limit: 500}
	for {
		evs, next, err := d.bus.Query(ctx, events.Filter{JobID: id}, page)
		if err != nil {
			return nil, err
		}
		all = append(all, evs...)
		if len(evs) < page.Limit || len(all) >= maxJobLog {
			return all, nil
		}
		page.Cursor = next.Cursor
	}
}

// jobExists answers 404 for an id the queue doesn't have. It returns false after writing the answer.
func (s *Server) jobExists(w http.ResponseWriter, r *http.Request, d *jobDeps, id int64) bool {
	_, err := d.queue.Get(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return false
	}
	if err != nil {
		internalError(w)
		return false
	}
	return true
}

// jobLog is GET /jobs/{id}/events: the job's log, oldest first.
func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	if d == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, ok := parseJobID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	if !s.jobExists(w, r, d, id) {
		return
	}
	evs, err := s.jobEvents(r.Context(), d, id)
	if err != nil {
		internalError(w)
		return
	}
	out := make([]logEventJSON, 0, len(evs))
	for _, e := range evs {
		out = append(out, logLine(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// jobHistory is GET /jobs/{id}/history: the job's lifecycle lines, oldest first. Debug lines are left out.
func (s *Server) jobHistory(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	if d == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, ok := parseJobID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	if !s.jobExists(w, r, d, id) {
		return
	}
	evs, err := s.jobEvents(r.Context(), d, id)
	if err != nil {
		internalError(w)
		return
	}
	out := []historyJSON{}
	for _, e := range evs {
		if !historyKinds[e.Kind] || e.Level == events.LevelDebug {
			continue
		}
		out = append(out, historyJSON{Time: timeJSON2(e.TS), Level: string(e.Level), Text: e.Message})
	}
	writeJSON(w, http.StatusOK, out)
}

// logLine is one stored event as the job log and the stream send it.
func logLine(e events.Event) logEventJSON {
	return logEventJSON{
		Seq: e.Seq, TS: timeJSON2(e.TS), Level: string(e.Level), Kind: string(e.Kind), Source: e.Source,
		Message: e.Message, JobID: idOrNil(e.JobID), LibraryID: strOrNil(e.LibraryID), WorkerID: strOrNil(e.WorkerID),
		NextAction: strOrNil(e.NextAction),
	}
}

// timeJSON2 is an event's time as RFC 3339 UTC with milliseconds.
func timeJSON2(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
