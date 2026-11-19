// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"net/http"

	"github.com/shrink-your-shit/shrink-your-shit/internal/notify"
)

// v2Notify is the notifications API: where the news goes, what it says, and a test. The address is write-only.
func v2Notify(api *http.ServeMux, svc *notify.Service) {
	api.HandleFunc("GET /api/v2/notifications", func(w http.ResponseWriter, r *http.Request) {
		s, err := svc.Get()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, s)
	})
	api.HandleFunc("PATCH /api/v2/notifications", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Summary  *notify.Summary `json:"summary"`
			LinkBase *string         `json:"linkBase"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		s, err := svc.SetSummary(in.Summary, in.LinkBase)
		if notifyError(w, err) {
			return
		}
		writeJSON(w, s)
	})
	api.HandleFunc("POST /api/v2/notifications/targets", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string              `json:"name"`
			Kind    string              `json:"kind"`
			URL     string              `json:"url"`
			Enabled *bool               `json:"enabled"`
			Events  *notify.EventsPatch `json:"events"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		t, err := svc.Create(notify.NewTarget{Name: in.Name, Kind: in.Kind, URL: in.URL, Enabled: in.Enabled, Events: in.Events})
		if notifyError(w, err) {
			return
		}
		writeJSON(w, t)
	})
	api.HandleFunc("PATCH /api/v2/notifications/targets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    *string             `json:"name"`
			URL     *string             `json:"url"`
			Enabled *bool               `json:"enabled"`
			Events  *notify.EventsPatch `json:"events"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		t, err := svc.Update(r.PathValue("id"), notify.TargetPatch{Name: in.Name, URL: in.URL, Enabled: in.Enabled, Events: in.Events})
		if notifyError(w, err) {
			return
		}
		writeJSON(w, t)
	})
	api.HandleFunc("DELETE /api/v2/notifications/targets/{id}", func(w http.ResponseWriter, r *http.Request) {
		if notifyError(w, svc.Delete(r.PathValue("id"))) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/v2/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			URL  string `json:"url"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		res, err := svc.Test(in.ID, in.Kind, in.URL)
		if notifyError(w, err) {
			return
		}
		writeJSON(w, map[string]any{"ok": res.OK, "message": res.Message})
	})
}

// notifyError answers a service error with the contract's code; it reports whether it answered.
func notifyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var bad notify.Invalid
	switch {
	case errors.As(err, &bad):
		apiError(w, http.StatusBadRequest, bad.Code, bad.Message)
	case errors.Is(err, notify.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "No notification target has that id.")
	default:
		serverError(w, err)
	}
	return true
}
