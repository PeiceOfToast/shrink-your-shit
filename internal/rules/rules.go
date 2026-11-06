// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package rules decides whether a probed file should be transcoded.
package rules

import (
	"fmt"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Rule matches files by source codec and minimum video bitrate.
// Default: H.264 above 4000 kbps -> HEVC.
type Rule struct {
	FromCodec      string `json:"fromCodec"`
	MinBitrateKbps int    `json:"minBitrateKbps"`
	CRF            int    `json:"crf"`
	Preset         string `json:"preset"`
}

// Default is the rule used when none is configured.
func Default() Rule {
	return Rule{FromCodec: "h264", MinBitrateKbps: 4000, CRF: 26, Preset: "medium"}
}

// Match reports whether the file should be transcoded, with a reason either way.
func (r Rule) Match(i media.Info) (bool, string) {
	if i.VideoCodec != r.FromCodec {
		return false, fmt.Sprintf("codec is %s, rule wants %s", i.VideoCodec, r.FromCodec)
	}
	if i.BitrateKbps < r.MinBitrateKbps {
		return false, fmt.Sprintf("bitrate %d kbps is below %d kbps", i.BitrateKbps, r.MinBitrateKbps)
	}
	return true, fmt.Sprintf("%s at %d kbps (>= %d kbps)", i.VideoCodec, i.BitrateKbps, r.MinBitrateKbps)
}
