// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package web serves the app's pages, its embedded assets and its JSON API.
package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web/next"
)

// Handler returns the HTTP handler: pages, assets under /assets and /healthz (all public), and JSON
// under /api, guarded by access.
func Handler(store *jobs.Store, access *Access, eng Engine) http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	mux.Handle("/api/", access.guard(api))
	mux.Handle("/api/worker/v1/", workerAPI(store, eng.Transport, eng.Version, eng)) // V7b1: remote workers, their own tokens
	access.routes(api)
	libraryRoutes(api, store, eng)
	systemRoutes(api, store, eng)
	ss := &settingsStore{store: store}
	settingsRoutes(api, ss, access, eng)
	flowRoutes(api, store, ss, eng)
	settingRoutes(api, store, eng)
	activityRoutes(api, store, ss, time.Now, eng)
	filesRoutes(api, store, eng) // S5, the Files view
	workerRoutes(api, store, ss, eng)
	previewRoutes(api, store, eng)
	wv := newWorkerView(store, ss, eng)
	v2Summary(api, store, wv)             // V3c: /api/v2/summary, processing and intake pauses
	v2EventStream(api, store, wv)         // V3a: /api/v2/events (live updates)
	v2Workers(api, wv)                    // V4a: /api/v2/workers
	v2Libraries(api, store, eng)          // V5b: /api/v2/libraries
	v2Jobs(api, store)                    // V2: /api/v2/jobs
	v2JobStreams(api, store, eng.RunLogs) // V3d: /api/v2/activity, per-job streams
	v2Workflows(api, store, eng)          // UI rebuild, V1: /api/v2 workflows (UI-API-CONTRACT-v2.md)
	// For health checks: answers without signing in and says nothing else.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	// One UI (user decision 2026-11-12, manager row qitem-20261112190001): the single-page app is the whole interface,
	// at /. Method-less "/" so the more specific /api/, /api/worker/v1/ and /healthz keep answering for themselves: the
	// app never answers an API path. The old pages and their assets are gone (git history keeps them).
	// the old interface's pages move to their place in the app (ui-shop's URL map) inside this handler, by exact path:
	// a router pattern for /libraries/ would make the router send /libraries to /libraries/ (a redirect loop)
	mux.Handle("/", oldPagesOr(appHandler(next.Files())))
	// the preview's address: /next/<path> moves to /<path> (bookmarks keep working), with its query
	mux.HandleFunc("GET /next/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, withQuery("/"+strings.TrimPrefix(r.URL.Path, "/next/"), r), http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /next", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, withQuery("/", r), http.StatusMovedPermanently)
	})
	api.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.List(200)
		if err != nil {
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		paths := []string{}
		for _, l := range libs {
			paths = append(paths, l.Path)
		}
		writeJSON(w, map[string]any{"libraries": paths, "jobs": list})
	})
	api.HandleFunc("POST /api/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		if err := store.Retry(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, jobs.ErrNotFound) {
				apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			} else {
				apiError(w, http.StatusConflict, "cannot_retry", "This job can't be tried again now: "+err.Error())
			}
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

// writeJSONStatus answers v as JSON with status. The Content-Type is set before WriteHeader: headers
// set after it are dropped, and the page only reads answers labelled JSON (a 201 without it left
// first-run without the new library's id).
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
