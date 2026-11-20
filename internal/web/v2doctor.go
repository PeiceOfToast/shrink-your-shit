// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// v2Doctor serves the Doctor page (UI-API-CONTRACT-v2.md "Doctor"): GET /api/v2/doctor. The encoder test is
// kept until ?retest=1; the other checks run on every call. It changes nothing.
func v2Doctor(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("GET /api/v2/doctor", func(w http.ResponseWriter, r *http.Request) {
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
		if r.URL.Query().Get("retest") == "1" {
			eng.Doctor.Retest(r.Context()) // first: the encoder below is the one conversions use after this test
		}
		inUse, _ := LocalEncoderFor(store, eng.Doctor)("libx265", false, nil) // "" when conversions use the processor
		in := doctor.APIInput{Version: eng.Version, DataDir: eng.DataDir, Libs: ls, InUse: inUse, Device: encoderDevice}
		writeJSON(w, eng.Doctor.API(r.Context(), in, false))
	})
}
