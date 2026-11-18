// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// ui-shop 12:32Z: a check's negative bitrate is a problem in the draft check, naming the step and field.
func TestV2DraftCheckValidatesConfig(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	f, _ := st.AddFlow("Paths", rules.SliderTemplate(3))
	d := workflow.FromTemplate(rules.SliderTemplate(3), nil)
	check := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeCheck, TypeVersion: 1, Name: "Big files", Enabled: true,
		Config: map[string]any{"minBitrateKbps": -5}}
	d.Nodes = append(d.Nodes, check)
	b, _ := json.Marshal(map[string]any{"workflow": d})
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workflows/" + strconv.FormatInt(f.ID, 10) + "/validate", body: string(b), ctype: "application/json"})
	if code != 200 || !strings.Contains(body, `"field":"minBitrateKbps"`) || !strings.Contains(body, `In 'Big files', “Bitrate is at least (kbps)” can't be less than 0.`) {
		t.Errorf("%d %s", code, body)
	}
}
