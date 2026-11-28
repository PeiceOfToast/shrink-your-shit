// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
)

// settingsOptions are the values the app supplies to the settings document: the running build and the trash folder
// every library keeps (the page shows it read-only).
func (s *Server) settingsOptions(rs *Deps) settings.Options {
	return settings.Options{Version: rs.AppVersion, TrashPath: safety.TrashDir}
}

// getSettings is GET /settings: the General, Processing and logs document (page 11).
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	doc, err := settings.Load(r.Context(), rs.DB, s.settingsOptions(rs))
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// patchSettings is PATCH /settings: only the changed fields are sent. The change is all or nothing; a refused field
// is a 400 with its code and field, and nothing is saved. Each saved change writes one settings.changed event.
func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	var body map[string]json.RawMessage
	if !decode(w, r, &body) {
		return
	}
	doc, changes, err := settings.Patch(r.Context(), rs.DB, s.settingsOptions(rs), body)
	if refusal, ok := settings.AsRefusal(err); ok {
		writeError(w, http.StatusBadRequest, refusal.Code, refusal.Message, refusal.Field, 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	if rs.Events != nil {
		for _, c := range changes {
			_ = rs.Events.Emit(events.Event{Level: events.LevelInfo, Kind: events.KindNote, Source: "settings",
				Code: "settings.changed", Message: fmt.Sprintf("%s: %s → %s", c.Key, c.Old, c.New)})
		}
	}
	writeJSON(w, http.StatusOK, doc)
}

// myLanguages is the person's languages from the settings (empty until they set some).
func (s *Server) myLanguages(r *http.Request) []string {
	rs := s.ready.Load()
	if rs == nil || rs.DB == nil {
		return []string{}
	}
	return settings.Languages(r.Context(), rs.DB)
}
