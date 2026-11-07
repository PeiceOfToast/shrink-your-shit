// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// systemRoutes are the setup checks: the folder check first-run runs as soon as a folder is chosen,
// and the health report Settings > System shows. Both use the doctor package, as the `doctor` command does.
func systemRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("POST /api/libraries/check", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		if !readJSON(w, r, &in) {
			return
		}
		p := strings.TrimSpace(in.Path)
		if p == "" || !filepath.IsAbs(p) {
			apiError(w, http.StatusBadRequest, "invalid_path", "Choose a folder by its full path.")
			return
		}
		if eng.Doctor == nil {
			apiError(w, http.StatusServiceUnavailable, "not_available", "The checks are not available.")
			return
		}
		writeJSON(w, eng.Doctor.Folder(r.Context(), filepath.Clean(p)))
	})
	api.HandleFunc("GET /api/system/health", func(w http.ResponseWriter, r *http.Request) {
		if eng.Doctor == nil {
			apiError(w, http.StatusServiceUnavailable, "not_available", "The checks are not available.")
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		var ls []doctor.Library
		for _, l := range libs {
			ls = append(ls, doctor.Library{ID: l.ID, Name: l.Name, Path: l.Path})
		}
		writeJSON(w, doctor.Health{Checks: eng.Doctor.Health(r.Context(), eng.DataDir, ls), Version: eng.Version, LogsTail: eng.Logs.Lines()})
	})
}
