// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// V3c (UI-API-CONTRACT-v2.md, Shell and dashboard): the dashboard's summary in one call, and the two
// pauses spec 7 keeps apart: processing (workers take nothing new; v1's queue pause) and intake (scans
// queue nothing new). Both pauses write their event to the live stream's sequence.
func v2Summary(api *http.ServeMux, store *jobs.Store, wv *workerView) {
	api.HandleFunc("GET /api/v2/summary", func(w http.ResponseWriter, r *http.Request) {
		_, seq, err := store.EventBounds() // first, so events after it reach a stream started from it
		if err != nil {
			serverError(w, err)
			return
		}
		week, err := store.FreedSince(time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339))
		if err != nil {
			serverError(w, err)
			return
		}
		libCounts, err := store.Counts()
		if err != nil {
			serverError(w, err)
			return
		}
		all, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		perLibrary := []map[string]any{} // every library, also with nothing saved yet
		for _, l := range all {
			c := libCounts[l.ID]
			perLibrary = append(perLibrary, map[string]any{"libraryId": strconv.FormatInt(l.ID, 10), "freed": c.FreedBytes, "waitingToFree": c.WaitingBytes, "couldFree": c.CouldFreeBytes})
		}
		local, err := wv.local(r.Context()) // V4a: the one worker there is today
		if err != nil {
			serverError(w, err)
			return
		}
		a, err := store.ActivitySummary()
		if err != nil {
			serverError(w, err)
			return
		}
		day := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
		ends, err := store.JobEndsSince(day)
		if err != nil {
			serverError(w, err)
			return
		}
		hours, err := store.Throughput(day)
		if err != nil {
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		days := 7
		if s, err := LoadSettings(store); err == nil {
			days = s.KeepOriginalsDays
		}
		list := func(card string, limit int) ([]map[string]any, map[string]int, error) {
			rows, _, counts, err := store.FileList(jobs.FileFilter{Show: card, Limit: limit})
			if err != nil {
				return nil, nil, err
			}
			items := []map[string]any{}
			for _, row := range rows {
				items = append(items, v2JobItem(store, fileItem(store, libs, row, card, days), row, card))
			}
			return items, counts, nil
		}
		active, counts, err := list(jobs.CardProcessing, 50)
		if err != nil {
			serverError(w, err)
			return
		}
		failures, _, err := list(jobs.CardFailed, 5)
		if err != nil {
			serverError(w, err)
			return
		}
		failed := ends["failed"] + ends["kept"]
		done := ends["done"]
		var rate any // completed / (completed + failed) in the last 24 h; null when there were none
		if done+failed > 0 {
			rate = float64(done) / float64(done+failed)
		}
		jobCounts := map[string]int{}
		for t, c := range v2Tabs {
			jobCounts[t] = counts[c]
		}
		writeJSON(w, map[string]any{
			"seq":       seq,
			"badges":    map[string]any{"failed24h": failed, "workersOffline": 0, "pluginUpdates": 0}, // the local worker is never offline
			"activity":  map[string]any{"converting": a.WorkingFiles, "waiting": a.WaitingFiles, "workersRunning": runningWorkers(store)},
			"jobCounts": jobCounts,
			"last24h":   map[string]any{"completed": done, "failed": failed, "skipped": ends["skipped"], "successRate": rate},
			"bytes": map[string]any{"freed": a.FreedBytes, "waitingToFree": a.WaitingBytes, "couldFree": a.CouldFreeBytes,
				"freedLast7d": week, "perLibrary": perLibrary}, // V5c: dated, and per library
			"workers":          []any{local},
			"activeJobs":       active,
			"recentFailures":   failures,
			"throughput":       hours,
			"processingPaused": store.Paused(),
			"intakePaused":     store.IntakePaused(),
		})
	})
	// processing: v1's own pause handler (everything it does stays), under v2's name
	v1 := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rec := httptest.NewRecorder()
			req := r.Clone(r.Context())
			req.URL.Path = path
			api.ServeHTTP(rec, req)
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
		}
	}
	api.HandleFunc("POST /api/v2/processing/pause", v1("/api/queue/pause"))
	api.HandleFunc("POST /api/v2/processing/resume", v1("/api/queue/resume"))
	api.HandleFunc("POST /api/v2/intake/{verb}", func(w http.ResponseWriter, r *http.Request) {
		verb := r.PathValue("verb")
		if verb != "pause" && verb != "resume" {
			apiError(w, http.StatusNotFound, "not_found", "There is no such action.")
			return
		}
		if err := store.SetIntakePaused(verb == "pause"); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"intakePaused": strings.HasPrefix(verb, "pause")})
	})
}

// runningWorkers is how many workers are started (contract: a number): the local one, or none (V5b,
// ui-shop 01:31Z; remote workers come in V7).
func runningWorkers(store *jobs.Store) int {
	if store.Paused() {
		return 0
	}
	return 1
}
