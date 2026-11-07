// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// flowItem is a flow as GET /api/flows shows it (UI-API-CONTRACT.md "Flows").
type flowItem struct {
	ID        int64    `json:"id,string"` // ids are opaque strings in the API
	Name      string   `json:"name"`
	UsedBy    []string `json:"usedBy"`
	MadeWith  string   `json:"madeWith"` // slider|selectors|builder, or legacy (made from program flags)
	ChangedAt string   `json:"changedAt"`
}

func madeWith(kind string) string {
	switch kind {
	case rules.KindSlider, rules.KindSelectors, rules.KindLegacy:
		return kind
	}
	return "builder"
}

func flowRoutes(api *http.ServeMux, store *jobs.Store, ss *settingsStore, eng Engine) {
	view := func(f jobs.Flow, usedBy []string) flowItem {
		if usedBy == nil {
			usedBy = []string{}
		}
		return flowItem{ID: f.ID, Name: f.Name, UsedBy: usedBy, MadeWith: madeWith(f.Template.Kind), ChangedAt: f.Changed}
	}
	flowID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such flow.")
			return 0, false
		}
		return id, true
	}
	storeErr := func(w http.ResponseWriter, err error) {
		var inUse jobs.ErrFlowInUse
		switch {
		case errors.As(err, &inUse):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "flow_in_use",
				"message": "Libraries use this flow (" + strings.Join(inUse.UsedBy, ", ") + "), so it can't be deleted. Nothing was changed.",
				"usedBy":  inUse.UsedBy}})
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such flow.")
		case errors.Is(err, jobs.ErrNameTaken):
			apiError(w, http.StatusConflict, "flow_name_taken", "Another flow already has this name.")
		default:
			serverError(w, err)
		}
	}
	readName := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		var in struct{ Name string }
		if !readJSON(w, r, &in) {
			return "", false
		}
		n := strings.TrimSpace(in.Name)
		if n == "" {
			apiError(w, http.StatusBadRequest, "invalid_name", "Give the flow a name.")
			return "", false
		}
		return n, true
	}
	one := func(w http.ResponseWriter, status int, f jobs.Flow) {
		all, err := store.Flows()
		if err != nil {
			serverError(w, err)
			return
		}
		for _, fu := range all {
			if fu.ID == f.ID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(view(fu.Flow, fu.UsedBy))
				return
			}
		}
		storeErr(w, jobs.ErrNotFound)
	}

	api.HandleFunc("GET /api/flows", func(w http.ResponseWriter, r *http.Request) {
		all, err := store.Flows()
		if err != nil {
			serverError(w, err)
			return
		}
		items := []flowItem{}
		for _, fu := range all {
			items = append(items, view(fu.Flow, fu.UsedBy))
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})
	// The steps of a flow, as plain text, come with the Plan compiler (slice 16); until then none.
	api.HandleFunc("GET /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		f, err := store.GetFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"id": strconv.FormatInt(f.ID, 10), "name": f.Name, "madeWith": madeWith(f.Template.Kind), "steps": []any{}})
	})
	api.HandleFunc("POST /api/flows", func(w http.ResponseWriter, r *http.Request) {
		name, ok := readName(w, r)
		if !ok {
			return
		}
		f, err := store.AddFlow(name, rules.SliderTemplate(3)) // the default template: Balanced
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusCreated, f)
	})
	api.HandleFunc("PATCH /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		name, ok := readName(w, r)
		if !ok {
			return
		}
		f, err := store.RenameFlow(id, name)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusOK, f)
	})
	api.HandleFunc("POST /api/flows/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		f, err := store.DuplicateFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusCreated, f)
	})
	api.HandleFunc("DELETE /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		if err := store.DeleteFlow(id); err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	notYet := func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotImplemented, "not_available", "Importing and exporting flows comes in a later version.")
	}
	api.HandleFunc("POST /api/flows/import", notYet)
	api.HandleFunc("GET /api/flows/{id}/export", notYet)

	// Choosing another flow for a library: only when Settings has "a flow per library" on.
	api.HandleFunc("PUT /api/libraries/{id}/flow", func(w http.ResponseWriter, r *http.Request) {
		libID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || libID <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		}
		var in struct{ FlowID json.RawMessage }
		if !readJSON(w, r, &in) {
			return
		}
		fid, err := strconv.ParseInt(strings.Trim(string(in.FlowID), `"`), 10, 64) // the page sends it as a string
		if err != nil || fid <= 0 {
			apiError(w, http.StatusBadRequest, "invalid_flow", "flowId must be a flow's id.")
			return
		}
		ss.mu.Lock()
		s, err := ss.load()
		ss.mu.Unlock()
		if err != nil {
			serverError(w, err)
			return
		}
		if !s.PerLibraryFlow {
			apiError(w, http.StatusConflict, "per_library_flow_off", "Choosing a flow for a library needs \"A flow per library\" turned on in Settings. Nothing was changed.")
			return
		}
		if _, err := store.GetLibrary(libID); err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		}
		if _, err := store.SetLibraryFlow(libID, fid); err != nil {
			storeErr(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow() // its files are looked at again with the new flow
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
