// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// v2Tabs are the Queue page's tabs and the S5 cards they read (one tab at a time).
var v2Tabs = map[string]string{"waiting": jobs.CardUnprocessed, "running": jobs.CardProcessing, "completed": jobs.CardProcessed,
	"skipped": jobs.CardSkipped, "failed": jobs.CardFailed}

// v2Jobs are V2's /api/v2 job routes (UI-API-CONTRACT-v2.md, Jobs): the queue by tab with spec 7's
// states, a job's detail with its steps and history, and bulk actions. They read S5's Files list and
// the job records; nothing about how jobs run changed.
func v2Jobs(api *http.ServeMux, store *jobs.Store) {
	keepDays := func() int {
		if s, err := LoadSettings(store); err == nil {
			return s.KeepOriginalsDays
		}
		return 7
	}
	api.HandleFunc("GET /api/v2/jobs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tab := q.Get("tab")
		if tab == "" {
			tab = "waiting"
		}
		card, ok := v2Tabs[tab]
		if !ok {
			apiError(w, http.StatusBadRequest, "invalid", "tab is waiting, running, completed, skipped or failed.")
			return
		}
		limit, offset := pageOf(r)
		f := jobs.FileFilter{Show: card, Q: q.Get("q"), Limit: limit, Offset: offset}
		switch q.Get("sort") { // queue: the Waiting tab in the order workers take files (design SCREENS V6)
		case "":
		case "queue":
			if tab != "waiting" {
				apiError(w, http.StatusBadRequest, "invalid", "sort=queue is for the waiting tab.")
				return
			}
			f.QueueOrder = true
		default:
			apiError(w, http.StatusBadRequest, "invalid", "sort is queue, or left out.")
			return
		}
		if lib := q.Get("libraryId"); lib != "" {
			id, err := strconv.ParseInt(lib, 10, 64)
			if err != nil || id <= 0 {
				apiError(w, http.StatusBadRequest, "invalid", "libraryId must be a library's id.")
				return
			}
			f.LibraryID = id
		}
		rows, total, counts, err := store.FileList(f)
		if err != nil {
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		days := keepDays()
		var positions map[int64]int
		if tab == "waiting" { // each waiting job's place in the queue (1: taken next), over all of it; null when not taken now
			ids := []int64{}
			for _, row := range rows {
				ids = append(ids, row.JobID)
			}
			if positions, err = store.QueuePositions(ids); err != nil {
				serverError(w, err)
				return
			}
		}
		items := []map[string]any{}
		for _, row := range rows {
			it := v2JobItem(store, fileItem(store, libs, row, card, days), row, card)
			if tab == "waiting" {
				it["position"] = nil
				if p, ok := positions[row.JobID]; ok {
					it["position"] = p
				}
			}
			items = append(items, it)
		}
		tabCounts := map[string]int{}
		for t, c := range v2Tabs {
			tabCounts[t] = counts[c]
		}
		writeJSON(w, map[string]any{"items": items, "total": total, "counts": tabCounts})
	})

	// Convert again (design 11:45Z): a done job runs again from its original with its workflow as it is now.
	api.HandleFunc("POST /api/v2/jobs/{id}/convert-again", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		err = store.ConvertAgain(id)
		var ae jobs.AgainError
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
		case errors.As(err, &ae):
			writeErrorWith(w, http.StatusConflict, ae.Code, ae.Message, map[string]any{"deletedAt": nilIfEmpty(ae.DeletedAt)})
		case err != nil:
			serverError(w, err)
		default:
			writeJSON(w, map[string]any{"ok": true, "jobId": strconv.FormatInt(id, 10)})
		}
	})

	api.HandleFunc("GET /api/v2/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		events, err := store.JobEvents(id)
		if err != nil {
			serverError(w, err)
			return
		}
		j, err := store.Get(id)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, jobs.ErrNotFound) {
			if len(events) == 0 {
				apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
				return
			}
			// its row went when it was skipped or stopped (as before V2); its history stays, and a job its
			// worker skipped keeps its steps (and log) and tries too, while its file's record names it (V6 review)
			last := events[len(events)-1]
			steps, err := store.Steps(id)
			if err != nil {
				serverError(w, err)
				return
			}
			d := map[string]any{"id": strconv.FormatInt(id, 10), "state": v2State(last.To, nil, nil), "reason": reasonOf(last),
				"events": v2Events(events), "steps": steps, "attempts": last.Attempts, "removed": true, "file": nil}
			for _, e := range events { // its file as recorded when it went (ui-shop 10:54Z: not "Job 8")
				if e.Path != "" {
					d["file"] = map[string]any{"path": e.Path, "name": filepath.Base(e.Path), "folder": filepath.Dir(e.Path)}
				}
			}
			writeJSON(w, d)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		steps, err := store.Steps(id)
		if err != nil {
			serverError(w, err)
			return
		}
		d := map[string]any{"id": strconv.FormatInt(id, 10), "state": v2State(string(j.Status), steps, livePercent(id)),
			"file": map[string]any{"path": j.Path, "bytes": j.SizeBefore}, "attempts": j.Attempts,
			"retryAt": nilIfEmpty(j.RetryAt), "error": nilIfEmpty(j.Error), "worker": nilIfEmpty(j.Worker),
			"createdAt": j.Created, "updatedAt": j.Updated, "steps": steps, "events": v2Events(events), "removed": false,
			"path": []string{}}
		if route, err := store.Route(id); err == nil && route != nil { // V6a: the nodes the file went along
			d["path"] = route
		}
		if j.Status == jobs.Queued && j.RetryAt != "" {
			d["state"] = "retry_wait"
		}
		d["logState"], d["failure"] = logState(store, id), nil // user review 11-13 item E
		if j.Status == jobs.Failed {
			d["failure"] = failureView(store, id)
		}
		d["reason"] = nil // a failure, a kept original or a retry: its code and words, from the change that set them
		if n := len(events); j.Error != "" && n > 0 {
			d["reason"] = reasonOf(events[n-1])
		}
		meta := map[string]any{"before": nil, "after": nil}
		var snap rules.Snapshot
		if json.Unmarshal([]byte(j.Plan), &snap) == nil && snap.Source != nil {
			meta["before"] = snap.Source // the original as probed when the job was planned
		} else if j.OutputPath == "" || j.OutputPath != j.Path {
			// a job planned before that was recorded: its original's probe is still the one at its
			// name only when the new file went elsewhere; after a replace at the same name it would be
			// the new file's, so before stays null (dev-review 04:27Z)
			if raw, ok := store.LatestProbe(j.Path); ok {
				meta["before"] = json.RawMessage(raw)
			}
		}
		if j.OutputPath != "" {
			if raw, ok := store.LatestProbe(j.OutputPath); ok {
				meta["after"] = json.RawMessage(raw)
			}
		}
		d["metadata"] = meta
		d["convertAgain"] = convertAgainOf(store, id, keepDays())
		if lib, err := store.GetLibrary(j.LibraryID); err == nil {
			d["library"] = map[string]any{"id": strconv.FormatInt(lib.ID, 10), "name": lib.Name}
			// the revision it runs with: a waiting job (never started, or back to retry) runs the newest published
			// at its next start; one that started ran the one published then (lead 22:23Z, 22:41Z)
			var at string
			for _, e := range events {
				if e.To == string(jobs.Running) {
					at = e.At
				}
			}
			if j.Status == jobs.Queued {
				if _, err := store.EnsureRevision(lib.FlowID); err == nil { // an old Save's template counts too
					at = time.Now().UTC().Format(time.RFC3339)
				}
			}
			if rev, ok, err := store.RevisionAt(lib.FlowID, at); err == nil && ok {
				d["workflow"] = map[string]any{"id": strconv.FormatInt(lib.FlowID, 10), "revisionId": strconv.FormatInt(rev.ID, 10), "version": rev.Number}
			}
		}
		writeJSON(w, d)
	})

	api.HandleFunc("POST /api/v2/jobs/actions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action   string
			IDs      []string
			Priority *int
			Pin      *string // reprioritize: "first" | "last" | "none" (design 03:22Z: wins over library priority)
		}
		if !readJSON(w, r, &in) {
			return
		}
		v1 := map[string]string{"retry": "retry", "requeue": "retry", "cancel": "stop"}[in.Action]
		if v1 == "" && in.Action != "reprioritize" || in.Action == "reprioritize" && (in.Priority == nil) == (in.Pin == nil) || len(in.IDs) == 0 || len(in.IDs) > 500 {
			apiError(w, http.StatusBadRequest, "invalid", "action is retry, requeue, cancel or reprioritize (with priority or pin), for 1 to 500 ids. Nothing was changed.")
			return
		}
		if in.Pin != nil && *in.Pin != "first" && *in.Pin != "last" && *in.Pin != "none" ||
			in.Priority != nil && (*in.Priority < -jobs.MaxLibraryPriority || *in.Priority > jobs.MaxLibraryPriority) {
			apiError(w, http.StatusBadRequest, "invalid", "pin is first, last or none; priority is a whole number from -1000 to 1000. Nothing was changed.")
			return
		}
		results := []map[string]any{}
		for _, sid := range in.IDs {
			res := map[string]any{"id": sid, "ok": true}
			id, err := strconv.ParseInt(sid, 10, 64)
			switch {
			case err != nil || id <= 0:
				res["ok"], res["code"], res["message"] = false, "not_found", "There is no such job."
			case in.Action == "reprioritize":
				var err error
				if in.Pin != nil {
					err = store.SetPin(id, strings.TrimPrefix(*in.Pin, "none"))
				} else {
					err = store.SetPriority(id, *in.Priority)
				}
				if err != nil {
					res["ok"], res["code"], res["message"] = false, "not_waiting", "Only a waiting job can be moved in the queue."
				}
			case v1 == "retry" && isQueued(store, id): // waiting already (review 66806b3): a retry wait goes now
				if err := store.RetryNow(id); err != nil {
					res["ok"], res["code"], res["message"] = false, "already_waiting", "This job is already waiting in the queue."
				}
			default: // v1's own handler, with all its checks: cancel never deletes anything
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/api/jobs/"+sid+"/"+v1, strings.NewReader("{}")).WithContext(r.Context())
				req.Header.Set("Content-Type", "application/json")
				for _, c := range r.Cookies() {
					req.AddCookie(c)
				}
				api.ServeHTTP(rec, req)
				if rec.Code >= 300 {
					var e struct {
						Error struct{ Code, Message string }
					}
					json.Unmarshal(rec.Body.Bytes(), &e)
					res["ok"], res["code"], res["message"] = false, e.Error.Code, e.Error.Message
				}
			}
			results = append(results, res)
		}
		writeJSON(w, map[string]any{"results": results})
	})
}

// v2JobItem is a Files item in the contract's Job terms: its state (spec 7), sub-state and progress.
func v2JobItem(store *jobs.Store, it map[string]any, row jobs.FileRow, card string) map[string]any {
	it["origin"] = "job"
	// ui-shop's First/Last chip reads pin from the row; priority is the job's place among the unpinned (its
	// library's, for a pinned one), never the reserved numbers that keep a pin
	it["priority"], it["pin"] = row.Priority, nil
	if pin := jobs.PinOf(row.Priority); pin != "" {
		it["pin"], it["priority"] = pin, 0
		if l, err := store.GetLibrary(row.LibraryID); err == nil {
			it["priority"] = l.Priority
		}
	}
	// reason: {code, message} | null, as every v2 reason (v1's item keeps the skip code as a string):
	// a skip's code and words, or a failure's, kept original's or retry's (ui-shop 23:27Z)
	switch {
	case card == jobs.CardSkipped:
		it["reason"] = map[string]string{"code": row.Reason, "message": it["detail"].(string)}
	case row.ErrorCode != "" && (card == jobs.CardFailed || card == jobs.CardUnprocessed && row.RetryAt != ""):
		it["reason"] = map[string]string{"code": row.ErrorCode, "message": row.Error}
	default:
		it["reason"] = nil
	}
	// user review 11-13 item E: why a failed job failed, and whether its log is in the database
	it["failure"], it["logState"] = nil, logState(store, row.JobID)
	if card == jobs.CardFailed {
		it["failure"] = failureView(store, row.JobID)
	}
	switch card {
	case jobs.CardUnprocessed:
		it["state"], it["subState"] = "queued", "waiting"
		if row.RetryAt != "" {
			it["state"], it["subState"] = "retry_wait", "retrying"
		} else if l, err := store.GetLibrary(row.LibraryID); err == nil && !l.Started {
			it["subState"] = "library_disabled" // its library is paused (V5b; design: "Library paused")
		} else if store.Paused() {
			it["subState"] = "processing_paused"
		}
	case jobs.CardProcessing:
		steps, _ := store.Steps(row.JobID)
		pct := livePercent(row.JobID)
		it["state"] = v2State(string(jobs.Running), steps, pct)
		it["subState"] = map[string]string{"preparing": "probing", "running": "converting", "validating": "checking", "committing": "saving"}[it["state"].(string)]
		it["progress"] = v2Progress(it["state"].(string), row.JobID, pct)
	case jobs.CardProcessed:
		it["state"], it["subState"] = "completed", nil
	case jobs.CardFailed:
		it["state"], it["subState"] = "failed", nil
	case jobs.CardSkipped:
		it["origin"], it["state"], it["subState"], it["priority"], it["pin"] = "scan", "skipped", nil, nil, nil // no job: no priority
		it["jobId"] = nil                                                                                       // the job its worker skipped (a workflow's skip after it failed): GET /jobs/{id} shows its steps, tries and log
		if row.JobID != 0 {
			it["jobId"] = strconv.FormatInt(row.JobID, 10)
		}
		if row.Reason == jobs.UserStoppedReason {
			it["state"] = "cancelled"
		}
	}
	return it
}

// v2State is spec 7's state for a job's status: a running one by the last step it reached.
func v2State(status string, steps []jobs.Step, percent *float64) string {
	switch status {
	case string(jobs.Queued):
		return "queued"
	case string(jobs.Done):
		return "completed"
	case string(jobs.Failed), string(jobs.Kept):
		return "failed"
	case "skipped", "cancelled":
		return status
	case string(jobs.Running):
		last := ""
		for _, s := range steps {
			last = s.Step
		}
		switch {
		case last == "verified":
			return "committing"
		case last == "converted":
			return "validating"
		case percent != nil:
			return "running"
		}
		return "preparing"
	}
	return status
}

func livePercent(id int64) *float64 {
	if l, ok := worker.Live(id); ok {
		p := l.Progress.Percent
		return &p
	}
	return nil
}

// v2Progress is truthful progress (spec 7): the step's own percent, and the whole job's from the
// steps done (probing 0-5, converting 5-85, checking 90, saving 97), never ffmpeg's as the job's.
func v2Progress(state string, id int64, pct *float64) map[string]any {
	p := map[string]any{"stage": map[string]string{"preparing": "Looking at the file", "running": "Converting video",
		"validating": "Checking the result", "committing": "Saving the result"}[state], "percent": nil, "workflowPercent": 0,
		"elapsedS": nil, "speed": nil, "etaS": nil, "fps": nil, "bitrateKbps": nil}
	switch state {
	case "preparing":
		p["workflowPercent"] = 2
	case "validating":
		p["workflowPercent"] = 90
	case "committing":
		p["workflowPercent"] = 97
	}
	if l, ok := worker.Live(id); ok {
		pr := l.Progress
		p["elapsedS"] = int(time.Since(l.Started).Seconds())
		if state == "running" && pct != nil {
			p["percent"], p["workflowPercent"] = *pct, 5+int(*pct*0.8)
		}
		if pr.Speed > 0 {
			p["speed"] = pr.Speed
		}
		if pr.ETASeconds >= 0 {
			p["etaS"] = int(pr.ETASeconds + 0.5)
		}
		if pr.FPS > 0 {
			p["fps"] = pr.FPS
		}
		if pr.BitrateKbps > 0 {
			p["bitrateKbps"] = int(pr.BitrateKbps + 0.5)
		}
	}
	return p
}

func v2Events(evs []jobs.JobEvent) []map[string]any {
	out := []map[string]any{}
	for _, e := range evs {
		var from any
		if e.From != nil {
			from = eventState(*e.From)
		}
		out = append(out, map[string]any{"seq": e.Seq, "at": e.At, "from": from, "to": eventState(e.To),
			"reason": reasonOf(e), "workerId": nilIfEmpty(e.Worker)}) // {code, message} | null, as everywhere in v2
	}
	return out
}

// eventState is a history entry's state: a status change records "running" as such (the finer running
// states come from the steps, which the history doesn't hold).
func eventState(status string) string {
	if status == string(jobs.Running) {
		return "running"
	}
	return v2State(status, nil, nil)
}

func reasonOf(e jobs.JobEvent) any {
	if e.Reason == "" {
		return nil
	}
	return map[string]string{"code": e.Reason, "message": e.Message}
}

func isQueued(store *jobs.Store, id int64) bool {
	j, err := store.Get(id)
	return err == nil && j.Status == jobs.Queued
}

// convertAgainOf is a job detail's convertAgain: whether Convert again can run, and if not, why (the
// drawer's muted line, e.g. "Can't convert again: the original was deleted on 12 November 2026.").
func convertAgainOf(store *jobs.Store, id int64, keepDays int) map[string]any {
	c, err := store.ConvertAgainCheck(id, keepDays)
	if err != nil {
		return map[string]any{"possible": false, "reason": nil, "policy": nil, "keptUntil": nil, "outputPath": nil}
	}
	a := map[string]any{"possible": c.Possible, "reason": nil, // what the run did, so the drawer can word the button
		"policy": nilIfEmpty(c.Policy), "keptUntil": nilIfEmpty(c.KeptUntil), "outputPath": nilIfEmpty(c.OutputPath)}
	if !c.Possible {
		a["reason"] = map[string]any{"code": c.Code, "message": c.Message, "deletedAt": nilIfEmpty(c.DeletedAt)}
	}
	return a
}
