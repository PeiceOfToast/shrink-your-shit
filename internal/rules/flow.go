// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
)

// A flow says which files of a library are converted and how. Until the flow graph engine (slice
// 30), a flow's behaviour is its template: a kind plus parameters, compiled per file into a Plan
// (plan.go). The slider table below is the one place its numbers live.

// Flow template kinds.
const (
	KindSlider    = "slider"    // the Basic slider: Params.Quality 1..5
	KindSelectors = "selectors" // the Standard selectors (slice 25)
	KindLegacy    = "legacy"    // the values given as program flags (-crf, -preset, ...), as before libraries had flows
	KindCustom    = "custom"    // a flow no editor can show (builder, import; slice 30)
)

// CompilerVersion is the version of the template-to-settings mapping below. A flow records the
// version it was made with, so a later change to the table does not silently change it.
const CompilerVersion = 1

// Template is what a flow stores about how it was made.
type Template struct {
	Kind            string     `json:"kind"`
	Params          Params     `json:"params"`
	Selectors       *Selectors `json:"selectors,omitempty"` // a selectors flow's choices (selectors.go)
	CompilerVersion int        `json:"compilerVersion"`
	// Languages are the person's languages (Settings > General, ISO 639-2 codes, comma-separated) for
	// "Keep only my languages": a program-wide setting, filled in when the flow is read, never stored in it.
	Languages string `json:"-"`
}

// Params are a template's values. A slider template uses Quality; a legacy one the rule fields
// and Output.
type Params struct {
	Quality        int    `json:"quality,omitempty"`
	FromCodec      string `json:"fromCodec,omitempty"`
	MinBitrateKbps int    `json:"minBitrateKbps,omitempty"`
	CRF            int    `json:"crf,omitempty"`
	Preset         string `json:"preset,omitempty"`
	Output         string `json:"output,omitempty"`
}

// SliderPosition is one position of the Basic slider: the recipe TIERS-PLAN.md 2.1 gives it. The API
// shows position, name and crf (UI-API-CONTRACT.md addendum 4); the rest is the engine's. There is no
// minimum saving: a checked conversion is always kept, even if it is not smaller (USER-REVIEW
// 2026-11-07, "always standardize").
type SliderPosition struct {
	Position int     `json:"position"`
	Name     string  `json:"name"`
	CRF      int     `json:"crf"` // at 1080p; one lower up to 720p, one higher at 2160p
	MinSSIM  float64 `json:"-"`   // the picture threshold
	MinBPP   float64 `json:"-"`   // eligible only above this many bits per pixel per frame
	// SaveLowPct..SaveHighPct is the Preview's estimate of how much of a converted file's video is
	// saved (DESIGN/FIRST-RUN.md: always a range). Anchored on the published reports in TIERS-PLAN.md
	// (about 10-55% for real content at Balanced), not measured on real files yet (D8).
	SaveLowPct, SaveHighPct int `json:"-"`
}

// Slider is the slider table (TIERS-PLAN.md 2.1). Every number is an untuned starting value (D8): it
// changes after calibration on real files, which is why the UI reads it from the API. This is the one
// place these numbers live.
var Slider = []SliderPosition{
	{1, "Smallest file", 27, 0.93, 0.05, 15, 60},
	{2, "Smaller", 25, 0.94, 0.065, 12, 57},
	{3, "Balanced", 23, 0.95, 0.08, 10, 55},
	{4, "Better quality", 21, 0.965, 0.10, 8, 40},
	{5, "Best quality", 19, 0.975, 0.12, 5, 25},
}

// SliderSources are the video codecs a slider flow converts (D5): H.264 and older formats.
var SliderSources = map[string]string{
	"h264": "H.264", "mpeg2video": "MPEG-2", "mpeg4": "MPEG-4", "msmpeg4v3": "MPEG-4 (DivX 3)",
	"vc1": "VC-1", "wmv1": "WMV", "wmv2": "WMV", "wmv3": "WMV",
}

// alreadyEfficient are the formats never converted: re-encoding them loses quality for little gain.
var alreadyEfficient = map[string]string{"hevc": "HEVC", "av1": "AV1", "vp9": "VP9"}

// DefaultSliderPosition is where a new library's slider starts: Balanced.
const DefaultSliderPosition = 3

// SliderPreset is the x265 preset every slider position uses.
const SliderPreset = "medium"

// SliderTemplate is a slider flow at position quality (1..5).
func SliderTemplate(quality int) Template {
	return Template{Kind: KindSlider, Params: Params{Quality: quality}, CompilerVersion: CompilerVersion}
}

// LegacyTemplate keeps a rule and output mode exactly as given.
func LegacyTemplate(r Rule, output string) Template {
	return Template{Kind: KindLegacy, CompilerVersion: CompilerVersion, Params: Params{
		FromCodec: r.FromCodec, MinBitrateKbps: r.MinBitrateKbps, CRF: r.CRF, Preset: r.Preset, Output: output}}
}

// NearMinBPP: a source within this factor of its position's MinBPP is the kind the minimum saving most
// often keeps, so the Preview counts its saving from 0 at the low end.
const NearMinBPP = 1.5

// EstimatePosition is the slider position whose estimate band applies to a template: its own position,
// or for a legacy flow the position with the nearest CRF (between two, the one that saves less).
func (t Template) EstimatePosition() SliderPosition {
	if p, ok := SliderAt(t.Params.Quality); ok && t.Kind == KindSlider {
		return p
	}
	if s, ok := t.SelectorsView(); ok {
		p, _ := SliderAt(s.Quality)
		return p
	}
	best := Slider[DefaultSliderPosition-1]
	for _, p := range Slider {
		if abs(p.CRF-t.Params.CRF) <= abs(best.CRF-t.Params.CRF) { // a tie goes to the smaller saving
			best = p
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// SliderAt returns the slider position q (1..5).
func SliderAt(q int) (SliderPosition, bool) {
	for _, p := range Slider {
		if p.Position == q {
			return p, true
		}
	}
	return SliderPosition{}, false
}

// Label is how a library card names its flow: settingKind and settingLabel in the API. Legacy flows
// show as "custom" until the editors can show them (slices 16/25).
func (t Template) Label() (kind, label string) {
	if t.Kind == KindSlider {
		if p, ok := SliderAt(t.Params.Quality); ok {
			return KindSlider, p.Name
		}
	}
	if s, ok := t.SelectorsView(); ok { // the quality's name, the word the slider would show (addendum 6)
		p, _ := SliderAt(s.Quality)
		return KindSelectors, p.Name
	}
	return KindCustom, "Custom"
}

func outputOr(o string) string {
	if strings.TrimSpace(o) == "" {
		return "replace"
	}
	return o
}
