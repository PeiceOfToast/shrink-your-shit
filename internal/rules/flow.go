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
	Kind            string `json:"kind"`
	Params          Params `json:"params"`
	CompilerVersion int    `json:"compilerVersion"`
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

// SliderPosition is one position of the Basic slider.
type SliderPosition struct {
	Position     int    `json:"position"`
	Name         string `json:"name"`
	CRF          int    `json:"crf"`
	MinSavingPct int    `json:"minSavingPct"`
}

// Slider is the slider table (TIERS-PLAN.md 2.1, UI-API-CONTRACT.md addendum 4). Untuned starting
// values: they change after calibration on real files, which is why the UI reads them from the API.
var Slider = []SliderPosition{
	{1, "Smallest", 27, 20},
	{2, "Smaller", 25, 15},
	{3, "Balanced", 23, 10},
	{4, "Better quality", 21, 8},
	{5, "Best quality", 19, 5},
}

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
	return KindCustom, "Custom"
}

func outputOr(o string) string {
	if strings.TrimSpace(o) == "" {
		return "replace"
	}
	return o
}
