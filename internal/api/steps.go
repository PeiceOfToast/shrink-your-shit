// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// The steps group: page 07's cards, built from the step registry and each settings struct's tags.
// The page has no per-step code, so this route is the entire flow editor's form.

// stepDefinition is one step: what page 07 needs to draw its card and to show what the step did
// in the Job log's words.
type stepDefinition struct {
	Type    string      `json:"type"`
	Version int         `json:"version"`
	Name    string      `json:"name"`
	Help    string      `json:"help"`
	Events  []stepEvent `json:"events"`
	// Form is the JSON Schema of the step's settings plus the layout hints under x-ui.
	Form flow.Form `json:"form"`
}

// stepEvent is one code a step can emit, with the words the log builds from it.
type stepEvent struct {
	Code   string       `json:"code"`
	Level  events.Level `json:"level"`
	Text   string       `json:"text"`
	Fields []string     `json:"fields,omitempty"`
	Keep   events.Keep  `json:"keep"`
}

// getStepDefinitions serves every registered step in registration order (Video, Audio, Save),
// which is page 07's step order.
func (s *Server) getStepDefinitions(w http.ResponseWriter, r *http.Request) {
	defs := flow.Definitions()
	out := make([]stepDefinition, 0, len(defs))
	for _, def := range defs {
		// Stage 3 fills the env from the workers; until then it is empty, so every choice is
		// offered. A card the workers lack is greyed by the check route, not by the form.
		form, err := flow.BuildForm(def, flow.Env{})
		if err != nil {
			// A tag the builder cannot read is a programming mistake, not a request problem.
			writeError(w, http.StatusInternalServerError, "internal", "A step's form could not be built.", "", 0)
			return
		}
		row := stepDefinition{Type: def.Type, Version: def.Version, Name: def.Name, Help: def.Help, Form: form}
		row.Events = make([]stepEvent, 0, len(def.Events))
		for _, e := range def.Events {
			row.Events = append(row.Events, stepEvent{Code: e.Code, Level: e.Level, Text: e.Text, Fields: e.Fields, Keep: e.Keep})
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}
