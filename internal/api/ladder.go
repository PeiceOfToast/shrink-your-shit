// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// The ladder group: page 07's recommended bitrates and the Quality slider's numbers. The page
// keeps no copy of either table (page 07, "Recommended numbers").

// getBitrateLadder serves the one table the editor and the engine both read.
func (s *Server) getBitrateLadder(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, flow.Ladder())
}
