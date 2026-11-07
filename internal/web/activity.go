// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// The Done and Kept originals lists (UI-API-CONTRACT.md "Activity", slice 17). Restore and purge are
// slice 18.
func activityRoutes(api *http.ServeMux, store *jobs.Store, ss *settingsStore, now func() time.Time, scanNow func()) {
	page := func(r *http.Request) (limit, offset int) {
		limit, offset = 50, 0
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, 500)
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
			offset = n
		}
		return
	}
	// deleteOn is when an original kept at keptAt is deleted under the setting now (the purge uses the
	// setting now too, so lowering it shows at once); daysLeft rounds the time left up to whole days.
	deleteOn := func(keptAt string, days int) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, keptAt)
		return t.Add(time.Duration(days) * 24 * time.Hour), err == nil
	}
	daysLeft := func(keptAt string, days int) *int {
		t, ok := deleteOn(keptAt, days)
		if !ok {
			return nil
		}
		d := max(0, int(math.Ceil(t.Sub(now()).Hours()/24)))
		return &d
	}
	keepDays := func() int {
		if s, err := ss.load(); err == nil {
			return s.KeepOriginalsDays
		}
		return 7
	}

	api.HandleFunc("GET /api/activity/done", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := page(r)
		items, total, err := store.DoneItems(limit, offset)
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		days := keepDays()
		for _, d := range items {
			// beside: the original was never touched, so it is still there ("kept", with no trash entry)
			state, keptID, left := "deleted", any(nil), (*int)(nil)
			switch {
			case d.State == jobs.OriginalKept:
				state, keptID, left = "kept", strconv.FormatInt(d.KeptID, 10), daysLeft(d.KeptAt, days)
			case d.State == jobs.OriginalRestored:
				state = "restored" // the original is back; the converted file is gone
			case d.Beside:
				state = "kept"
			}
			out = append(out, map[string]any{"jobId": strconv.FormatInt(d.JobID, 10), "library": d.Library, "path": d.Path,
				"beforeBytes": d.BeforeBytes, "afterBytes": d.AfterBytes, "originalState": state, "keptDaysLeft": left, "keptId": keptID})
		}
		writeJSON(w, map[string]any{"items": out, "total": total})
	})

	api.HandleFunc("GET /api/activity/kept", func(w http.ResponseWriter, r *http.Request) {
		items, err := store.KeptItems()
		if err != nil {
			serverError(w, err)
			return
		}
		s, err := ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		var bytes int64
		out := []map[string]any{}
		for _, k := range items {
			bytes += k.Bytes
			on := ""
			if t, ok := deleteOn(k.KeptAt, s.KeepOriginalsDays); ok {
				on = t.UTC().Format(time.RFC3339)
			}
			out = append(out, map[string]any{"keptId": strconv.FormatInt(k.ID, 10), "library": k.Library, "path": k.Path,
				"originalBytes": k.Bytes, "nowBytes": k.NewBytes, "deleteOn": on})
		}
		writeJSON(w, map[string]any{"keptBytes": bytes, "count": len(items), "days": s.KeepOriginalsDays, "items": out})
	})

	// Get original back (the one restore, after the page's confirm). force is for the command line
	// only (restore --force): the page never sends it.
	api.HandleFunc("POST /api/kept/{keptId}/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Force bool }
		if !readJSON(w, r, &in) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("keptId"), 10, 64)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such kept original. Nothing was changed.")
			return
		}
		o, err := worker.Restore(store, id, in.Force)
		var re *worker.RestoreError
		switch {
		case errors.As(err, &re) && re.Code == "not_found":
			apiError(w, http.StatusNotFound, re.Code, re.Message)
			return
		case errors.As(err, &re):
			apiError(w, http.StatusConflict, re.Code, re.Message)
			return
		case err != nil:
			serverError(w, err)
			return
		}
		confirmed := worker.IdentityConfirmed(o)
		msg := "The original is back, and its identity is confirmed (same size, time and content samples as when it was kept). It will not be converted again."
		if !confirmed {
			msg = "The original is back, but its identity could not be confirmed (its size, time or content samples differ from when it was kept). It will not be converted again."
		}
		writeJSON(w, map[string]any{"ok": true, "path": o.Path, "identityConfirmed": confirmed, "message": msg})
	})

	// Home's figures (slice 21b): the only source of savings numbers (contract F1).
	api.HandleFunc("GET /api/activity/summary", func(w http.ResponseWriter, r *http.Request) {
		a, err := store.ActivitySummary()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"freedBytes": a.FreedBytes, "waitingBytes": a.WaitingBytes, "workingFiles": a.WorkingFiles,
			"waitingFiles": a.WaitingFiles, "paused": false, "counts": map[string]int{"skipped": a.Skipped, "failed": a.Failed}})
	})

	// What is converting now, and what waits per library.
	api.HandleFunc("GET /api/activity/queue", func(w http.ResponseWriter, r *http.Request) {
		working, err := store.WorkingJobs()
		if err != nil {
			serverError(w, err)
			return
		}
		next, err := store.UpNext()
		if err != nil {
			serverError(w, err)
			return
		}
		s, err := ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for _, j := range working {
			phase, eta := "converting", any(nil)
			if j.Progress >= 100 {
				phase = "checking" // the new file is written; the safety checks run
			} else if t, err := time.Parse(time.RFC3339, j.Started); err == nil && j.Progress > 0 {
				// time left from the time taken so far and the share done
				eta = int(math.Round(now().Sub(t).Seconds() * (100 - j.Progress) / j.Progress))
			}
			out = append(out, map[string]any{"jobId": strconv.FormatInt(j.ID, 10), "library": j.LibraryName, "path": j.Path,
				"phase": phase, "percent": math.Round(j.Progress), "etaSeconds": eta, "fromBytes": j.SizeBefore, "expectedBytes": nil})
		}
		up := []map[string]any{}
		for _, q := range next {
			up = append(up, map[string]any{"library": q.Library, "count": q.Count, "order": s.QueueOrder})
		}
		writeJSON(w, map[string]any{"working": out, "upNext": up})
	})

	// Failed conversions and originals a check kept (Retry: POST /api/jobs/{id}/retry).
	api.HandleFunc("GET /api/activity/failed", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := page(r)
		items, total, err := store.FailedJobs(limit, offset)
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for _, j := range items {
			title := "Converting did not finish"
			if j.Status == jobs.Kept {
				title = "A check said no, so the original was kept"
			}
			out = append(out, map[string]any{"jobId": strconv.FormatInt(j.ID, 10), "library": j.LibraryName, "path": j.Path,
				"title": title, "detail": j.Error, "originalUntouched": true})
		}
		writeJSON(w, map[string]any{"items": out, "total": total})
	})

	// Files the scanner left alone, with why (?reason= one code).
	api.HandleFunc("GET /api/activity/skipped", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := page(r)
		items, total, err := store.SkippedFiles(r.URL.Query().Get("reason"), limit, offset)
		if err != nil {
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for _, k := range items {
			lib, libID := "", any(nil)
			for _, l := range libs { // the library whose folder holds the file
				if rel, err := filepath.Rel(l.Path, k.Path); err == nil && !strings.HasPrefix(rel, "..") {
					lib, libID = l.Name, strconv.FormatInt(l.ID, 10)
				}
			}
			detail := rules.SkipWords[k.Reason]
			if detail == "" {
				detail = strings.TrimPrefix(k.Decision, "skip: ")
			}
			out = append(out, map[string]any{"id": strconv.FormatInt(k.ID, 10), "library": lib, "path": k.Path,
				"reason": k.Reason, "detail": upperFirst(detail) + ".", "libraryId": libID})
		}
		writeJSON(w, map[string]any{"items": out, "total": total})
	})

	// Convert again: forget the file's decision; the next scan looks at it afresh (and may skip it
	// again for the same reason: this is not "force").
	api.HandleFunc("POST /api/skipped/{id}/convert-again", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err == nil {
			_, err = store.ForgetSkipped(id)
		}
		switch {
		case errors.Is(err, jobs.ErrNotFound) || id <= 0:
			apiError(w, http.StatusNotFound, "not_found", "That file is not in the skipped list any more. Nothing was changed.")
			return
		case err != nil:
			serverError(w, err)
			return
		}
		if scanNow != nil {
			scanNow()
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	api.HandleFunc("POST /api/system/backup", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotImplemented, "not_available", "Backups come in a later update. Nothing was downloaded.")
	})
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
