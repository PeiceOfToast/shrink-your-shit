// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// systemRoutes are the setup checks: the folder check first-run runs as soon as a folder is chosen,
// and the health report Settings > About shows. Both use the doctor package, as the `doctor` command does.
func systemRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("POST /api/libraries/check", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path, OutputFolder string // outputFolder: where the workflow will save new files, if elsewhere
			// NextToLibrary: the workflow saves next to each library (first run's default): is there a
			// usable folder beside this one? Name is the library's, for the words.
			NextToLibrary bool
			Name          string
		}
		if !readJSON(w, r, &in) {
			return
		}
		p := strings.TrimSpace(in.Path)
		if p == "" || !filepath.IsAbs(p) || jobs.DevicePath(p) {
			apiError(w, http.StatusBadRequest, "invalid_path", "Choose a folder by its full path.")
			return
		}
		if eng.Doctor == nil {
			apiError(w, http.StatusServiceUnavailable, "not_available", "The checks are not available.")
			return
		}
		rep := eng.Doctor.Folder(r.Context(), filepath.Clean(p))
		if b := outputInsideBlocker(strings.TrimSpace(in.OutputFolder), filepath.Clean(p)); b != nil {
			rep.OK, rep.Blockers = false, append(rep.Blockers, *b)
		}
		if in.NextToLibrary { // before the library exists (first run)
			name := strings.TrimSpace(in.Name)
			if name == "" {
				name = filepath.Base(filepath.Clean(p))
			}
			if _, problem := jobs.NextToLibrary(name, filepath.Clean(p)); problem != nil {
				rep.OK, rep.Blockers = false, append(rep.Blockers, doctor.Issue{Code: problem.Code, Message: problem.Message, Folder: problem.Folder, Reason: problem.Reason})
			}
		}
		writeJSON(w, rep)
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
		checks := eng.Doctor.Health(r.Context(), eng.DataDir, ls)
		paused, err := encoderPauseChecks(store, time.Now())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, doctor.Health{Checks: append(checks, paused...), Version: eng.Version, LogsTail: eng.Logs.Lines()})
	})
}

// encoderPauseChecks warns about each worker whose graphics encoder is paused after three failures in a row.
// A pause starts at a fall-back, so one in the last hour covers every pause still on.
func encoderPauseChecks(store *jobs.Store, now time.Time) ([]doctor.Check, error) {
	fallbacks, err := store.EncoderFallbacksSince(now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	var out []doctor.Check
	for _, f := range fallbacks {
		until, paused, err := store.EncoderPaused(f.Worker)
		if err != nil {
			return nil, err
		}
		if !paused {
			continue
		}
		name := workerName(store, f.Worker)
		out = append(out, doctor.Check{ID: "encoder:" + f.Worker, Label: "Graphics encoder paused: " + name, Status: "warn",
			Detail: name + "'s graphics encoder failed three times in a row, so its files use the processor until " +
				until.Local().Format("15:04") + "."})
	}
	return out, nil
}

// workerName is what a person calls a worker: this computer's pool (its id starts "builtin"), else a paired
// remote worker's name, else its id.
func workerName(store *jobs.Store, id string) string {
	if strings.HasPrefix(id, "builtin") {
		if cfg, err := store.WorkerConfigOf(jobs.LocalWorker); err == nil {
			return cfg.Name
		}
		return "This computer"
	}
	if rws, err := store.RemoteWorkers(); err == nil {
		for _, rw := range rws {
			if rw.ID == id && rw.Name != "" {
				return rw.Name
			}
		}
	}
	return id
}
