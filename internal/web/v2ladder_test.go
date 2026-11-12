// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-12 item 1: the page shows the engine's own numbers for each resolution.
func TestV2BitrateLadder(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/bitrate-ladder"})
	var l struct {
		Unit          string
		Rows          []struct{ Row, Label string }
		Formats       map[string]map[string]map[string]float64
		ResolutionRow map[string]string
	}
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatal(err)
	}
	if l.Unit != "Mbps" || len(l.Rows) != 5 || l.Rows[4].Label != "480p and below" || l.ResolutionRow["480"] != "sd" {
		t.Errorf("%s", body)
	}
	if got := l.Formats["hevc"]["720"]; got["low"] != rules.Ladder["hevc"]["low"]["720"] || got["medium"] != 2.5 || got["high"] != 4 || got["rough"] != rules.Rough["hevc"]["720"] {
		t.Errorf("hevc 720p %v", got)
	}
	if l.Formats["vp9"]["1080"]["medium"] != rules.Ladder["hevc"]["medium"]["1080"] {
		t.Errorf("vp9 uses hevc's: %v", l.Formats["vp9"]["1080"])
	}
}
