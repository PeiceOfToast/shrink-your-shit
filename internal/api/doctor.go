// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
)

// getDoctor is GET /doctor: the checks the app runs on itself (page 11). It serves the last run, and says it is
// still starting until there is one; ?retest=1 runs them again. A change of a check's status writes one health
// event in the system log (the doctor's own job), which is what feeds the attention row.
func (s *Server) getDoctor(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if rs.Doctor == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "The health check isn't running yet.", "", 0)
		return
	}
	res, ok := rs.Doctor.Report(r.Context(), r.URL.Query().Get("retest") == "1")
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
