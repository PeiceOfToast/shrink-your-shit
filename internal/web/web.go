// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package web serves the app's pages, its embedded assets and its JSON API.
package web

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web/next"
)

//go:embed static
var static embed.FS

// Handler returns the HTTP handler: pages, assets under /assets and /healthz (all public), and JSON
// under /api, guarded by access.
func Handler(store *jobs.Store, access *Access, eng Engine) http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	mux.Handle("/api/", access.guard(api))
	access.routes(api)
	libraryRoutes(api, store, eng)
	systemRoutes(api, store, eng)
	ss := &settingsStore{store: store}
	settingsRoutes(api, ss, access)
	flowRoutes(api, store, ss, eng)
	settingRoutes(api, store, eng)
	activityRoutes(api, store, ss, time.Now, eng)
	filesRoutes(api, store, eng) // S5, the Files view
	workerRoutes(api, store, ss, eng)
	previewRoutes(api, store, eng)
	v2Workflows(api, store, eng) // UI rebuild, V1: /api/v2 workflows (UI-API-CONTRACT-v2.md)
	// For health checks: answers without signing in and says nothing else.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	sub, _ := fs.Sub(static, "static")
	files := http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /assets/", gzipAssets("/assets/", sub, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // no directory listing
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
	// The new single-page app (UI rebuild), public like these pages; its data comes from /api.
	mux.HandleFunc("GET /next/", nextApp(next.Files()))
	mux.Handle("GET /next", http.RedirectHandler("/next/", http.StatusMovedPermanently))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		to := "/libraries/"
		if r.URL.RawQuery != "" { // keep ?token=: the printed sign-in link points at the root
			to += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, to, http.StatusFound)
	})
	items := loadNav()
	for _, p := range pages {
		mux.HandleFunc("GET "+p.Path+"{$}", pageHandler(items, p))
	}
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
