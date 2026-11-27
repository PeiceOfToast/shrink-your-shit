// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// streamBatch is how many stored events one catch-up read takes.
const streamBatch = 200

// pollEvery is how often an idle stream checks the database even with no wake, in case a wake was missed.
const pollEvery = time.Second

// streamFrame writes one SSE frame. id is empty for a frame that must not move Last-Event-ID (ping).
func streamFrame(w io.Writer, id, kind string, data []byte) error {
	var err error
	if id != "" {
		_, err = fmt.Fprintf(w, "id: %s\n", id)
		if err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	return err
}

// stream is GET /api/v2/events, or GET /api/v2/events?job=<id> for one job's log. Each stored event is one frame:
// id is the event's row id (the job's seq with ?job=), event is its kind, data is the log line. Last-Event-ID
// resumes after that id from the database, then goes live, so a reconnect has no gap and no repeat. A ping every
// 15 s carries no id, so it never moves Last-Event-ID. The stream ends when the client goes or the server stops.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	d := s.jobs.Load()
	if rs == nil || d == nil || d.bus == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		internalError(w)
		return
	}

	ctx := r.Context()
	filter := events.Filter{}
	job := r.URL.Query().Get("job")
	if job != "" {
		id, ok := parseJobID(job)
		if !ok {
			writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
			return
		}
		if _, err := d.queue.Get(ctx, id); errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
			return
		} else if err != nil {
			internalError(w)
			return
		}
		filter.JobID = id
	}
	// A job's ids are its seq; the global stream's ids are row ids.
	idOf := func(e events.Event) string {
		if filter.JobID != 0 {
			return strconv.FormatInt(e.Seq, 10)
		}
		return strconv.FormatInt(e.RowID, 10)
	}

	cursor, err := resumeCursor(ctx, d.bus, filter, r.Header.Get("Last-Event-ID"))
	if err != nil {
		internalError(w)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The subscription is only a wake-up: what a client gets always comes from the database, so the live path and
	// the replay path are one. A dropped subscription ends the stream; the browser reconnects and resumes.
	wake, unsub := d.bus.Subscribe(filter)
	defer unsub()
	ping := time.NewTicker(s.pingInterval())
	defer ping.Stop()
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()

	for {
		next, err := catchUp(ctx, d.bus, filter, cursor, func(e events.Event) error {
			name := logKind(e.Kind)
			if filter.JobID == 0 {
				// The global stream speaks the contract's names; other job-log kinds stay on ?job=.
				n, ok := contractKind(e.Kind)
				if !ok {
					return nil
				}
				name = n
			}
			data, err := json.Marshal(logLine(e))
			if err != nil {
				return err
			}
			return streamFrame(w, idOf(e), name, data)
		})
		if err != nil {
			return // a failed write or read ends the stream; the browser reconnects
		}
		cursor = next
		flusher.Flush()

		select {
		case <-ctx.Done():
			return
		case <-s.stopped:
			return
		case e, open := <-wake:
			if !open {
				return
			}
			// Progress is live only: it is never stored, so the subscription is the one way it reaches a client.
			// It goes to a job's stream with no id line, so it never moves Last-Event-ID.
			if e.Kind == events.KindProgress && filter.JobID != 0 {
				data, err := json.Marshal(logLine(e))
				if err != nil {
					return
				}
				if err := streamFrame(w, "", string(events.KindProgress), data); err != nil {
					return
				}
				flusher.Flush()
			}
		case <-poll.C:
		case <-ping.C:
			if err := streamFrame(w, "", "ping", []byte("{}")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// resumeCursor is where the stream starts. With a Last-Event-ID it is that id, so the stream replays after it. A
// new stream starts at the newest stored event, so it sends only what comes next.
func resumeCursor(ctx context.Context, bus *events.Bus, f events.Filter, lastID string) (int64, error) {
	if n, err := strconv.ParseInt(lastID, 10, 64); err == nil && n >= 0 {
		return n, nil
	}
	evs, _, err := bus.Query(ctx, f, events.Page{Limit: 1, Newest: true})
	if err != nil {
		return 0, err
	}
	if len(evs) == 0 {
		return 0, nil
	}
	if f.JobID != 0 {
		return evs[0].Seq, nil
	}
	return evs[0].RowID, nil
}

// catchUp sends every stored event after cursor, in order, and returns the new cursor. Ids are the row id, or the
// seq for a job, so a page is read by the same key the frames carry.
func catchUp(ctx context.Context, bus *events.Bus, f events.Filter, cursor int64, send func(events.Event) error) (int64, error) {
	for {
		evs, next, err := bus.Query(ctx, f, events.Page{Limit: streamBatch, Cursor: cursor})
		if err != nil {
			return cursor, err
		}
		for _, e := range evs {
			if err := send(e); err != nil {
				return cursor, err
			}
		}
		cursor = next.Cursor
		if len(evs) < streamBatch {
			return cursor, nil
		}
	}
}

// pingInterval is how often an idle stream pings. Tests shorten it.
func (s *Server) pingInterval() time.Duration {
	if s.pingEvery > 0 {
		return s.pingEvery
	}
	return 15 * time.Second
}

// CloseStreams ends every open event stream. The server calls it before it shuts its listener down, since a
// stream never ends on its own.
func (s *Server) CloseStreams() {
	s.closeOnce.Do(func() { close(s.stopped) })
}

// contractKind is the name the UI listens for on the global stream (real-client-contract.md §5, Q2). Queue lifecycle
// kinds map to job.queued or job.state; a job's end maps to job.done or job.failed. The scan kinds already carry the
// contract names. Anything else is not sent on the global stream.
func contractKind(k events.Kind) (string, bool) {
	switch k {
	case events.KindQueued:
		return "job.queued", true
	case events.KindPicked, events.KindRetry, events.KindRequeued, events.KindCancelled:
		return "job.state", true
	case events.KindDone, events.KindSkipped:
		return "job.done", true
	case events.KindFailed:
		return "job.failed", true
	case events.KindScanStart, events.KindScanProgress, events.KindScanEnd:
		return string(k), true
	}
	return "", false
}
