// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// v2Backups is item 5 (UI-API-CONTRACT-v2.md "Backup"): list, make, download and delete backups. Restoring
// one is a later part.
func v2Backups(api *http.ServeMux, store *jobs.Store, ss *settingsStore) {
	api.HandleFunc("GET /api/v2/backups", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.ListBackups()
		if err != nil {
			serverError(w, err)
			return
		}
		ss.mu.Lock()
		s, err := ss.load()
		ss.mu.Unlock()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"backups": list, "keep": s.BackupKeep})
	})
	api.HandleFunc("POST /api/v2/backups", func(w http.ResponseWriter, r *http.Request) {
		b, err := store.WriteBackup(jobs.ReasonManual)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, b)
	})
	api.HandleFunc("GET /api/v2/backups/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		path, b, err := store.BackupFile(r.PathValue("id"))
		if errors.Is(err, jobs.ErrNoBackup) {
			apiError(w, http.StatusNotFound, "not_found", "There is no backup with that id.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			serverError(w, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+b.Name+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		io.Copy(w, f)
	})
	api.HandleFunc("DELETE /api/v2/backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := store.DeleteBackup(r.PathValue("id"))
		if errors.Is(err, jobs.ErrNoBackup) {
			apiError(w, http.StatusNotFound, "not_found", "There is no backup with that id.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
