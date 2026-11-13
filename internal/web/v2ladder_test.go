// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strings"
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
		VBRFormats    []string
	}
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatal(err)
	}
	if l.Unit != "Mbps" || len(l.Rows) != 5 || l.Rows[4].Label != "480p and below" || l.ResolutionRow["480"] != "sd" {
		t.Errorf("%s", body)
	}
	if got := l.Formats["hevc"]["720"]; got["low"] != rules.Ladder["hevc"]["low"]["720"] || got["medium"] != 2.4 || got["high"] != 3.6 || got["rough"] != rules.Rough["hevc"]["720"] {
		t.Errorf("hevc 720p %v", got)
	}
	if strings.Join(l.VBRFormats, ",") != "hevc,h264,vp9" {
		t.Errorf("VBR is not offered for AV1: %v", l.VBRFormats)
	}
	if l.Formats["vp9"]["1080"]["medium"] != rules.Ladder["hevc"]["medium"]["1080"] {
		t.Errorf("vp9 uses hevc's: %v", l.Formats["vp9"]["1080"])
	}
}

// User review 11-13 item A: the slider's settings come from the engine's table, one per position, with the
// factors the page's note uses (50-60 fps +40%, HDR +25%).
func TestV2BitrateLadderSlider(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/bitrate-ladder"})
	var l struct {
		Factors map[string]float64
		Slider  []struct {
			Position, CRF   int
			Name, Format    string
			KeepsResolution bool
			CapKbps         *int
			SaveLowPct      int     `json:"saveLowPct"`
			SaveHighPct     int     `json:"saveHighPct"`
			SSIMMin         float64 `json:"ssimMin"`
		}
	}
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Slider) != 5 || l.Slider[2].Name != "Balanced" || l.Slider[2].CRF != 23 || l.Slider[2].Format != "hevc" ||
		!l.Slider[2].KeepsResolution || l.Slider[2].CapKbps != nil || l.Slider[2].SaveLowPct != 10 || l.Slider[2].SaveHighPct != 55 {
		t.Errorf("slider: %+v", l.Slider)
	}
	if l.Factors["fps60"] != 1.4 || l.Factors["hdr"] != 1.25 {
		t.Errorf("factors: %v", l.Factors)
	}
}
