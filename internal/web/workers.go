// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Workers (S2, USER-REVIEW 2026-11-07: nothing converts until the person starts a worker). Today there is
// one, "this computer" (id this): the worker pool. Started or stopped is the paused flag, which Claim
// honours in the same statement, so no job is taken while it is stopped; scanning and the Preview go on.
// "Stop after these files" sets it and lets the running files finish; "Stop now" also puts them back in
// the queue (Pause all, slice 24). Files at once is Settings' parallelJobs (1-8), which the pool follows.

const thisWorker = "this"

func workerRoutes(api *http.ServeMux, store *jobs.Store, ss *settingsStore, eng Engine) {
	host, _ := os.Hostname()
	worker := func(ctx context.Context) (map[string]any, error) {
		working, err := store.WorkingJobs()
		if err != nil {
			return nil, err
		}
		s, err := ss.load()
		if err != nil {
			return nil, err
		}
		converting := []map[string]any{}
		for _, j := range working {
			eta := any(nil) // from the time taken so far and the share done, as Activity's queue says it
			if t, err := time.Parse(time.RFC3339, j.Started); err == nil && j.Progress > 0 && j.Progress < 100 {
				eta = int(math.Round(time.Since(t).Seconds() * (100 - j.Progress) / j.Progress))
			}
			converting = append(converting, map[string]any{"jobId": strconv.FormatInt(j.ID, 10), "path": j.Path,
				"percent": math.Round(j.Progress), "etaSeconds": eta})
		}
		var state string
		switch stopped := store.Paused(); {
		case stopped && len(converting) > 0:
			state = "stopping" // running files finish, nothing new is taken
		case stopped:
			state = "stopped"
		case len(converting) > 0:
			state = "converting"
		default:
			state = "idle" // running, waiting for files
		}
		var gpu any // null when the hardware check did not run: no checks, or no ffmpeg to run it with
		if eng.Doctor != nil {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			t := eng.Doctor.Tools(cctx)
			cancel()
			if t.FFmpeg != "" {
				found, vendor := doctor.GPU(t)
				v := any(nil)
				if vendor != "" {
					v = vendor
				}
				gpu = map[string]any{"found": found, "vendor": v}
			}
		}
		// quiet_hours (with quietUntil) and unreachable are reserved: quiet hours is slice 35, other
		// machines come later
		return map[string]any{"id": thisWorker, "name": "This computer", "host": host, "state": state,
			"filesAtOnce": s.ParallelJobs, "converting": converting, "quietUntil": nil, "gpu": gpu}, nil
	}
	one := func(w http.ResponseWriter, r *http.Request) {
		v, err := worker(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, v)
	}
	found := func(w http.ResponseWriter, r *http.Request) bool {
		if r.PathValue("id") != thisWorker {
			apiError(w, http.StatusNotFound, "not_found", "There is no such worker. Nothing was changed.")
			return false
		}
		return true
	}

	api.HandleFunc("GET /api/workers", func(w http.ResponseWriter, r *http.Request) {
		v, err := worker(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"workers": []map[string]any{v}, "stoppedByUpdate": store.StoppedByUpdate(),
			"basicFlowsChanged": store.BasicChangedByUpdate()})
	})

	api.HandleFunc("POST /api/workers/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		if err := startWorker(store); err != nil {
			serverError(w, err)
			return
		}
		one(w, r)
	})

	api.HandleFunc("POST /api/workers/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			When string `json:"when"`
		}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		switch in.When {
		case "", "after_current":
		case "now":
		default:
			apiError(w, http.StatusBadRequest, "invalid", "when must be after_current or now. Nothing was changed.")
			return
		}
		if err := store.SetPaused(true); err != nil {
			serverError(w, err)
			return
		}
		if in.When == "now" && eng.PauseNow != nil { // the running files go back to the queue, originals untouched
			eng.PauseNow()
		}
		one(w, r)
	})

	api.HandleFunc("PATCH /api/workers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			FilesAtOnce *int `json:"filesAtOnce"`
		}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		if in.FilesAtOnce != nil {
			if n := *in.FilesAtOnce; n < 1 || n > 8 {
				apiError(w, http.StatusBadRequest, "invalid_setting", "Enter a whole number from 1 to 8.")
				return
			}
			s, err := ss.load()
			if err != nil {
				serverError(w, err)
				return
			}
			s.ParallelJobs = *in.FilesAtOnce // the pool follows it live
			if err := ss.save(s); err != nil {
				serverError(w, err)
				return
			}
		}
		one(w, r)
	})

	// "Got it" on the one-time "Converting is stopped after the update" notice.
	api.HandleFunc("POST /api/workers/notice/dismiss", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if err := store.ClearStoppedByUpdate(); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"stoppedByUpdate": false})
	})
}

// startWorker starts this computer's worker; starting any worker ends the update notice for good.
func startWorker(store *jobs.Store) error {
	if err := store.SetPaused(false); err != nil {
		return err
	}
	return store.ClearStoppedByUpdate()
}
