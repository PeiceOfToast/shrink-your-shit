// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "sync"

// VideoFormat is how a Standard flow's video format is encoded (S6). CRFOffset moves the quality
// position's x265 CRF onto the encoder's own scale; the numbers are starting values to calibrate (D8),
// and the SSIM gate judges every result as before.
type VideoFormat struct {
	Name      string // in words
	Encoder   string // ffmpeg's encoder
	CRFOffset int
	Presets   map[string]string // speed (faster|normal|slower) → -preset; nil: Extra says it
	Extra     map[string][]string
	Tag       string // the sample-entry tag in MP4 ("" none)
}

// VideoFormats are the formats Standard offers (UI-API-CONTRACT.md, Workshop addition 21:10Z, item 1).
var VideoFormats = map[string]VideoFormat{
	"hevc": {Name: "HEVC", Encoder: "libx265", Presets: SpeedPresets, Tag: "hvc1"},
	"h264": {Name: "H.264", Encoder: "libx264", CRFOffset: -5, Presets: SpeedPresets, Tag: "avc1"}, // x264 CRF 23 ≈ x265 CRF 28
	"av1": {Name: "AV1", Encoder: "libsvtav1", CRFOffset: 9, Presets: map[string]string{"faster": "8", "normal": "6", "slower": "4"},
		Extra: map[string][]string{"": {"-svtav1-params", "tune=0"}}, Tag: "av01"},
	"vp9": {Name: "VP9", Encoder: "libvpx-vp9", CRFOffset: 10, Extra: map[string][]string{ // constant quality: -b:v 0
		"faster": {"-b:v", "0", "-deadline", "good", "-cpu-used", "4"},
		"normal": {"-b:v", "0", "-deadline", "good", "-cpu-used", "2"},
		"slower": {"-b:v", "0", "-deadline", "good", "-cpu-used", "1"}}},
}

// formatOrder is the order formats are offered in.
var formatOrder = []string{"hevc", "h264", "av1", "vp9"}

var availableMu sync.RWMutex

// SetAvailableFormats keeps the formats whose encoder this ffmpeg has (encoders: the names ffmpeg
// -encoders lists). HEVC is always offered: without libx265 nothing converts at all (the doctor says so).
func SetAvailableFormats(encoders map[string]bool) {
	out := []string{"hevc"}
	for _, f := range formatOrder[1:] {
		if encoders[VideoFormats[f].Encoder] {
			out = append(out, f)
		}
	}
	availableMu.Lock()
	AvailableFormats = out
	availableEncoders = encoders
	availableMu.Unlock()
}

// availableEncoders are this ffmpeg's encoders (nil until read: then every one counts as there).
var availableEncoders map[string]bool

// HasEncoder reports whether this ffmpeg has encoder name (true before the encoders were read).
func HasEncoder(name string) bool {
	availableMu.RLock()
	defer availableMu.RUnlock()
	return availableEncoders == nil || availableEncoders[name]
}

// Available is the formats this computer can make now.
func Available() []string {
	availableMu.RLock()
	defer availableMu.RUnlock()
	return append([]string(nil), AvailableFormats...)
}
