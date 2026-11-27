// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// The check the flow editor calls after each change (page 07): what blocks publishing, what is
// greyed and each closed card's line. All of it comes from flow.Validate; the page has no rules of
// its own.

// validateBody is the request: the flow as it stands in the editor, saved or not.
type validateBody struct {
	Flow flow.Flow `json:"flow"`
}

// validateWorkflow answers POST /api/v2/workflows/{id}/validate. The id names the workflow the
// page is editing; the check itself is pure, so the flow in the body is the whole input.
func (s *Server) validateWorkflow(w http.ResponseWriter, r *http.Request) {
	var body validateBody
	if !decode(w, r, &body) {
		return
	}
	// Stage 3 fills the env from the workers; until then it is empty, so no choice is greyed for a
	// missing encoder and no card is known (stage 3 item 3 changes only the env).
	writeJSON(w, http.StatusOK, flow.Validate(body.Flow, flow.ValidateContext{Env: flow.Env{}}))
}
