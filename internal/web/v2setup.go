// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// setupRoleKey is the app_meta key the install's role is kept under. It is not a setting: the settings
// PATCH cannot change it, only POST /api/v2/setup/role can.
const setupRoleKey = "setupRole"

// v2Setup is the install's role (design ONE-COMMAND-SETUP.md section 11): the first page asks "Main
// server" or "Worker for another server". This stores the role and serves it; joining a worker is a later
// slice, so "worker" is refused for now.
//   - GET /api/v2/setup: {role: "unset" | "server" | "worker", join: null}
//   - POST /api/v2/setup/role {role}: "server" or "unset" (409 role_change_busy with runningJobs while jobs
//     run); "worker" is 400 not_available; anything else is 400 invalid_role.
func v2Setup(api *http.ServeMux, store *jobs.Store, ss *settingsStore) {
	// current is the saved role. When none is saved, an install that already has a library or has got past
	// the first step predates the role, so it is a server and never sees the enrollment page.
	current := func() (string, error) {
		saved, _, err := store.Meta(setupRoleKey)
		if err != nil || saved != "" {
			return saved, err
		}
		s, err := ss.load()
		if err != nil {
			return "", err
		}
		libs, err := store.Libraries()
		if err != nil {
			return "", err
		}
		if len(libs) > 0 || s.FirstRunStep > 1 {
			return "server", nil
		}
		return "unset", nil
	}
	view := func(role string) map[string]any {
		return map[string]any{"role": role, "join": nil}
	}

	api.HandleFunc("GET /api/v2/setup", func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		role, err := current()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, view(role))
	})
	api.HandleFunc("POST /api/v2/setup/role", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Role string `json:"role"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		switch in.Role {
		case "server", "unset":
		case "worker":
			apiError(w, http.StatusBadRequest, "not_available", "Joining another server is not available yet. Nothing was changed.")
			return
		default:
			apiError(w, http.StatusBadRequest, "invalid_role", "The role is server or worker. Nothing was changed.")
			return
		}
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if in.Role == "unset" {
			working, err := store.WorkingJobs()
			if err != nil {
				serverError(w, err)
				return
			}
			if len(working) > 0 {
				writeErrorWith(w, http.StatusConflict, "role_change_busy",
					"Jobs are running, so the role can't be changed yet. Nothing was changed.", map[string]any{"runningJobs": len(working)})
				return
			}
		}
		if err := store.SetMeta(setupRoleKey, in.Role); err != nil {
			serverError(w, err)
			return
		}
		if err := store.RecordEvent("setup.changed", map[string]any{"type": "setup.changed", "role": in.Role}); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, view(in.Role))
	})
}
