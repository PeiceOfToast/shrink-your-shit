// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package web serves the app's pages, its embedded assets and its JSON API.
package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
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
	ss := &settingsStore{store: store}
	settingsRoutes(api, ss, access)
	flowRoutes(api, store, ss, eng)
	settingRoutes(api, store, eng)
	// For health checks: answers without signing in and says nothing else.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	sub, _ := fs.Sub(static, "static")
	files := http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /assets/", gzipAssets(sub, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // no directory listing
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
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
			http.Error(w, err.Error(), 500)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			http.Error(w, err.Error(), 500)
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
			http.Error(w, "bad id", 400)
			return
		}
		if err := store.Retry(id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
