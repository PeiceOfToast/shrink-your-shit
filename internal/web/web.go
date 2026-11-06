// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package web serves the status page and its JSON API.
package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

//go:embed static
var static embed.FS

// Handler returns the HTTP handler: static page at / and JSON under /api.
func Handler(store *jobs.Store, libraries []string) http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.List(200)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"libraries": libraries, "jobs": list})
	})
	mux.HandleFunc("POST /api/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
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
