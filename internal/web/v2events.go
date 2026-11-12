// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Live updates (V3a; UI-API-CONTRACT-v2.md, Live updates): GET /api/v2/events streams the events
// table as Server-Sent Events. Each message's id is its seq, the same sequence the database keeps, so
// a reconnect with Last-Event-ID replays exactly what was missed, across restarts too; only a gap the
// 30-day pruning removed (or a very long one) is answered with "resync", after which the page reloads
// its data. Each connection reads the table itself (an indexed read every 250 ms): one user, a few
// tabs, no shared state to get wrong.
var (
	eventPoll      = 250 * time.Millisecond
	eventKeepalive = 15 * time.Second
	eventProgress  = time.Second     // job.progress at most this often per job (contract), only when it changed
	eventHeartbeat = 5 * time.Second // worker.heartbeat (V4a)
)

const eventReplayMax = 10000 // a longer gap: resync instead of replaying it

func v2EventStream(api *http.ServeMux, store *jobs.Store, wv *workerView) {
	api.HandleFunc("GET /api/v2/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			apiError(w, http.StatusInternalServerError, "server_error", "This server can't stream events.")
			return
		}
		oldest, newest, err := store.EventBounds()
		if err != nil {
			serverError(w, err)
			return
		}
		startStream(w)

		from := r.Header.Get("Last-Event-ID")
		if from == "" {
			from = r.URL.Query().Get("since")
		}
		last, err := strconv.ParseInt(from, 10, 64)
		switch {
		case from == "" || err != nil || last < 0:
			last = newest
			send(w, newest, "hello", map[string]any{"seq": newest})
		case last > newest || (last < newest && (last+1 < oldest || newest-last > eventReplayMax)):
			// the gap is gone (pruned) or too long, or the id is ahead of the newest (the database was
			// restored or replaced; waiting for it would be silence): the page reloads its data
			last = newest
			send(w, newest, "resync", map[string]any{"seq": newest})
		}
		flusher.Flush()

		poll, keep, prog := time.NewTicker(eventPoll), time.NewTicker(eventKeepalive), time.NewTicker(eventProgress)
		beat := time.NewTicker(eventHeartbeat)
		defer poll.Stop()
		defer keep.Stop()
		defer prog.Stop()
		defer beat.Stop()
		sent := map[int64]string{} // what each running job's last progress message said
		for {
			select {
			case <-r.Context().Done():
				return
			case <-prog.C: // V3b: running jobs' progress, no id (not replayed: the page's data has it)
				now := map[int64]string{}
				for _, id := range worker.LiveIDs() {
					steps, _ := store.Steps(id)
					pct := livePercent(id)
					state := v2State(string(jobs.Running), steps, pct)
					data := map[string]any{"type": "job.progress", "jobId": strconv.FormatInt(id, 10), "state": state, "progress": v2Progress(state, id, pct)}
					b, _ := json.Marshal(data)
					if now[id] = string(b); sent[id] != now[id] {
						send(w, -1, "job.progress", data)
					}
				}
				sent = now
				flusher.Flush()
			case <-keep.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case <-beat.C: // V4a: the worker is there, with its slots (no id: not replayed)
				if wk, err := wv.local(r.Context()); err == nil {
					send(w, -1, "worker.heartbeat", map[string]any{"type": "worker.heartbeat", "workerId": wk["id"], "at": wk["lastHeartbeatAt"],
						"status": wk["status"], "slots": wk["slots"], "live": wk["live"]})
					flusher.Flush()
				}
			case <-poll.C:
				evs, err := store.EventsAfter(last, 500)
				if err != nil {
					return // the page reconnects and replays from its last id
				}
				for _, e := range evs {
					// V3d: an activity feed entry first, with no id of its own: a page that drops between
					// the two gets both again from the previous id (the entry's own id tells it apart)
					if a, ok := store.ActivityOf(e); ok {
						send(w, -1, "activity", map[string]any{"type": "activity", "event": a})
					}
					typ, data := eventMessage(e)
					send(w, e.Seq, typ, data)
					last = e.Seq
				}
				if len(evs) > 0 {
					flusher.Flush()
				}
			}
		}
	})
}

// eventMessage is an event as the contract's LiveEvent: a job's change of state now (from null: it was
// created). Kinds added later carry their own data.
func eventMessage(e jobs.Event) (string, map[string]any) {
	if e.Kind != "job" {
		var d map[string]any
		json.Unmarshal([]byte(e.Data), &d)
		return e.Kind, d
	}
	var from any
	if e.From != nil {
		from = eventState(*e.From)
	}
	m := map[string]any{"type": "job.state", "jobId": strconv.FormatInt(e.JobID, 10), "at": e.At, "from": from, "to": eventState(e.To),
		"reason": nil, "workerId": nilIfEmpty(e.Worker)}
	if e.Reason != "" {
		var d struct{ Message string }
		json.Unmarshal([]byte(e.Data), &d)
		m["reason"] = map[string]string{"code": e.Reason, "message": d.Message}
	}
	return "job.state", m
}

// startStream sends a stream's headers: never cached or held back by a proxy, and no write deadline.
func startStream(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // a proxy must not hold the stream back
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
}

// send writes one SSE message; id -1: none (job.progress, not replayed). Hello and resync carry the
// newest seq (0 for none yet), so a browser that drops right after them reconnects with Last-Event-ID
// and misses nothing.
func send(w http.ResponseWriter, id int64, event string, data any) {
	b, _ := json.Marshal(data)
	if id >= 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
