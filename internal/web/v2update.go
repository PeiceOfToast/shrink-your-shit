// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"runtime"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// v2Update is the update notice. GET /api/v2/update is the last check, kept (it asks GitHub for nothing);
// POST /api/v2/update/check asks now, at most one real request a minute.
func v2Update(api *http.ServeMux, ck *update.Checker, ss *settingsStore) {
	api.HandleFunc("GET /api/v2/update", func(w http.ResponseWriter, r *http.Request) {
		on, err := updatesOn(ss)
		if err != nil {
			serverError(w, err)
			return
		}
		st, err := update.Load(ss.store, ck.Current)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, updateBody(st, on))
	})
	api.HandleFunc("POST /api/v2/update/check", func(w http.ResponseWriter, r *http.Request) {
		on, err := updatesOn(ss)
		if err != nil {
			serverError(w, err)
			return
		}
		if !on {
			apiError(w, http.StatusConflict, "updates_off", "update checks are off")
			return
		}
		st, err := ck.Check(time.Minute)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, updateBody(st, true))
	})
}

// updatesOn reads the setting under the settings lock, as the settings handlers do.
func updatesOn(ss *settingsStore) (bool, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, err := ss.load()
	if err != nil {
		return false, err
	}
	return s.UpdateChecks(), nil
}

// updateBody is the notice as the API shows it. available is never true while checks are off.
func updateBody(st update.Status, on bool) map[string]any {
	checked := any(nil)
	if !st.CheckedAt.IsZero() {
		checked = st.CheckedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"enabled": on, "current": st.Current, "latest": emptyAsNil(st.Latest), "available": on && st.Available,
		"notes": st.Notes, "url": emptyAsNil(st.URL), "publishedAt": emptyAsNil(st.PublishedAt),
		"checkedAt": checked, "error": emptyAsNil(st.Error),
		"installType": update.InstallType(st.Current, runtime.GOOS, update.DetectEnv()),
	}
}

func emptyAsNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
