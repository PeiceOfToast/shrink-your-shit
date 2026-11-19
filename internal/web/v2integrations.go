// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"net/http"

	"github.com/shrink-your-shit/shrink-your-shit/internal/integrations"
)

// v2Integrations is the Sonarr and Radarr API: where they are, how to reach them, and a test. The key is write-only.
func v2Integrations(api *http.ServeMux, svc *integrations.Service) {
	api.HandleFunc("GET /api/v2/integrations", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, list)
	})
	api.HandleFunc("POST /api/v2/integrations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind         string                 `json:"kind"`
			Name         string                 `json:"name"`
			URL          string                 `json:"url"`
			APIKey       string                 `json:"apiKey"`
			PathMappings []integrations.Mapping `json:"pathMappings"`
			Enabled      *bool                  `json:"enabled"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		it, err := svc.Create(integrations.NewIntegration{Kind: integrations.Kind(in.Kind), Name: in.Name, URL: in.URL,
			APIKey: in.APIKey, PathMappings: in.PathMappings, Enabled: in.Enabled})
		if integrationError(w, err) {
			return
		}
		writeJSON(w, it)
	})
	api.HandleFunc("PATCH /api/v2/integrations/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name         *string                 `json:"name"`
			URL          *string                 `json:"url"`
			APIKey       *string                 `json:"apiKey"`
			PathMappings *[]integrations.Mapping `json:"pathMappings"`
			Enabled      *bool                   `json:"enabled"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		it, err := svc.Update(r.PathValue("id"), integrations.Patch{Name: in.Name, URL: in.URL, APIKey: in.APIKey,
			PathMappings: in.PathMappings, Enabled: in.Enabled})
		if integrationError(w, err) {
			return
		}
		writeJSON(w, it)
	})
	api.HandleFunc("DELETE /api/v2/integrations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if integrationError(w, svc.Delete(r.PathValue("id"))) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/v2/integrations/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID           string                  `json:"id"`
			Kind         string                  `json:"kind"`
			URL          *string                 `json:"url"`
			APIKey       *string                 `json:"apiKey"`
			PathMappings *[]integrations.Mapping `json:"pathMappings"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		res, err := svc.Test(integrations.TestIn{ID: in.ID, Kind: integrations.Kind(in.Kind), URL: in.URL,
			APIKey: in.APIKey, PathMappings: in.PathMappings})
		if integrationError(w, err) {
			return
		}
		writeJSON(w, res)
	})
}

// integrationError answers a refused request with its contract code; it reports whether it answered.
func integrationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var inv integrations.Invalid
	switch {
	case errors.As(err, &inv):
		apiError(w, http.StatusBadRequest, inv.Code, inv.Message)
	case errors.Is(err, integrations.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "There is no such integration.")
	default:
		serverError(w, err)
	}
	return true
}
