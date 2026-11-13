// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// V3d (UI-API-CONTRACT-v2.md, Live updates): the activity feed, and one job's own streams.
//   - GET /api/v2/activity?before=<id>&limit=<n> is the Dashboard's feed, newest first; the live stream
//     sends each new entry as `activity`.
//   - GET /api/v2/jobs/{id}/log/stream tails the job's run log: `job.log` {jobId, text, next}, its id the
//     byte offset next, so Last-Event-ID (or ?from=) goes on where it stopped.
//   - GET /api/v2/jobs/{id}/events is everything about that job: its changes of state (`job.state`, id
//     the seq: Last-Event-ID replays them), new steps (`job.step`), progress (`job.progress`, at most once a
//     second) and the log (`job.log`, no id: ?logFrom= goes on).
//
// Both job streams send `end` and close once the job is no longer waiting or converting and its log
// is all sent; a page that wants more (a retry) opens them again.
func v2JobStreams(api *http.ServeMux, store *jobs.Store) {
	api.HandleFunc("GET /api/v2/activity", func(w http.ResponseWriter, r *http.Request) {
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 || limit > 100 {
			limit = 20
		}
		items, total, err := store.ActivityFeed(before, limit)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"items": items, "total": total})
	})
	api.HandleFunc("GET /api/v2/jobs/{id}/log/stream", func(w http.ResponseWriter, r *http.Request) {
		id, ok := streamJob(w, r, store)
		if !ok {
			return
		}
		from := r.Header.Get("Last-Event-ID")
		if from == "" {
			from = r.URL.Query().Get("from")
		}
		at, _ := strconv.ParseInt(from, 10, 64)
		jobStream(w, r, store, id, at, false)
	})
	api.HandleFunc("GET /api/v2/jobs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		id, ok := streamJob(w, r, store)
		if !ok {
			return
		}
		at, _ := strconv.ParseInt(r.URL.Query().Get("logFrom"), 10, 64)
		jobStream(w, r, store, id, at, true)
	})
}

// streamJob is the job a per-job stream is for: one with a row or a history (a skipped job's row is
// gone, its history isn't); 404 otherwise.
func streamJob(w http.ResponseWriter, r *http.Request, store *jobs.Store) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		if _, gerr := store.Get(id); gerr != nil {
			if evs, _ := store.JobEvents(id); len(evs) == 0 {
				err = errors.New("no such job")
			}
		}
	}
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
		return 0, false
	}
	if _, ok := w.(http.Flusher); !ok {
		apiError(w, http.StatusInternalServerError, "server_error", "This server can't stream events.")
		return 0, false
	}
	return id, true
}

// jobStream streams job id's log from byte logAt, and with all, its changes, steps and progress too.
func jobStream(w http.ResponseWriter, r *http.Request, store *jobs.Store, id, logAt int64, all bool) {
	flusher := w.(http.Flusher)
	jobID := strconv.FormatInt(id, 10)
	var last int64 // the newest seq sent of this job's changes
	stepsSent := 0
	if all {
		evs, _ := store.JobEvents(id)
		if n := len(evs); n > 0 {
			last = evs[n-1].Seq
		}
		if s, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && s >= 0 && s < last {
			last = s // a reconnect: replay the changes after it
		}
		steps, _ := store.Steps(id)
		stepsSent = len(steps) // the page has them (GET /jobs/{id}); only new ones are sent
	}
	startStream(w)
	if all {
		send(w, last, "hello", map[string]any{"type": "hello", "jobId": jobID, "seq": last})
	}
	poll, keep, prog := time.NewTicker(eventPoll), time.NewTicker(eventKeepalive), time.NewTicker(eventProgress)
	defer poll.Stop()
	defer keep.Stop()
	defer prog.Stop()
	sentProgress := ""
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-prog.C:
			if !all {
				continue
			}
			if _, live := worker.Live(id); !live {
				continue
			}
			steps, _ := store.Steps(id)
			pct := livePercent(id)
			state := v2State(string(jobs.Running), steps, pct)
			data := map[string]any{"type": "job.progress", "jobId": jobID, "state": state, "progress": v2Progress(state, id, pct)}
			if b, _ := json.Marshal(data); string(b) != sentProgress {
				sentProgress = string(b)
				send(w, -1, "job.progress", data)
				flusher.Flush()
			}
		case <-poll.C:
			// whether the job has ended, asked BEFORE its changes are read: then the change that ended it
			// is already stored and sent below, before end (asked after, a job ending in between got end
			// without its last change: review-6f2b3a6, under load)
			ended := jobEnded(store, id)
			if all {
				evs, err := store.JobEvents(id)
				if err != nil {
					return
				}
				for _, e := range evs {
					if e.Seq > last {
						typ, data := eventMessage(jobs.Event{Seq: e.Seq, Kind: "job", JobID: id, At: e.At, From: e.From, To: e.To, Reason: e.Reason,
							Worker: e.Worker, Data: messageData(e.Message)})
						send(w, e.Seq, typ, data)
						last = e.Seq
					}
				}
				if jobEventsRead != nil {
					jobEventsRead()
				}
				steps, _ := store.Steps(id)
				for _, st := range steps[min(stepsSent, len(steps)):] {
					send(w, -1, "job.step", map[string]any{"type": "job.step", "jobId": jobID, "step": st})
				}
				stepsSent = len(steps)
			}
			for { // the log, in ReadMax pieces, until it is all sent
				text, next, err := logText(store, id, logAt)
				if err != nil || text == "" {
					break
				}
				logID := next
				if all {
					logID = -1 // the job stream's ids are its changes' seq
				}
				send(w, logID, "job.log", map[string]any{"type": "job.log", "jobId": jobID, "text": text, "next": next})
				logAt = next
			}
			flusher.Flush()
			if ended {
				if text, _, _ := logText(store, id, logAt); text == "" { // nothing written since
					send(w, -1, "end", map[string]any{"type": "end", "jobId": jobID})
					flusher.Flush()
					return
				}
			}
		}
	}
}

// jobEventsRead, if set, runs right after a job stream's poll has read the job's changes (tests: a job
// that ends there must still get its last change before end).
var jobEventsRead func()

// jobEnded reports whether job id is no longer waiting or converting (its row gone: skipped or removed).
func jobEnded(store *jobs.Store, id int64) bool {
	j, err := store.Get(id)
	return err != nil || (j.Status != jobs.Queued && j.Status != jobs.Running)
}

// messageData is a history entry's sentence as the events table holds it (data.message).
func messageData(msg string) string {
	if msg == "" {
		return ""
	}
	b, _ := json.Marshal(map[string]string{"message": msg})
	return string(b)
}
