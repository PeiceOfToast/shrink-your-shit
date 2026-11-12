// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"math"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Max bitrate levels (S6): a cap beside the quality, from research's ladder (RESEARCH/bitrate-ladder.md)
// for the new file's height and video format. Starting values to calibrate (D8).
var BitrateLevels = []string{"none", "high", "medium", "low"}

// Ladder is Mbps by format, level and height ("2160", "1440", "1080", "720", "sd"). VP9 has no column in
// research: it uses HEVC's (a similar efficiency).
var Ladder = map[string]map[string]map[string]float64{
	"hevc": {"low": {"2160": 8, "1440": 5, "1080": 3, "720": 1.5, "sd": 0.8}, "medium": {"2160": 12, "1440": 8, "1080": 5, "720": 2.5, "sd": 1.2},
		"high": {"2160": 20, "1440": 12, "1080": 8, "720": 4, "sd": 2}},
	"h264": {"low": {"2160": 16, "1440": 9, "1080": 5, "720": 2.5, "sd": 1.2}, "medium": {"2160": 25, "1440": 14, "1080": 8, "720": 4, "sd": 2},
		"high": {"2160": 40, "1440": 20, "1080": 12, "720": 6, "sd": 3}},
	"av1": {"low": {"2160": 6, "1440": 3.5, "1080": 2.2, "720": 1.1, "sd": 0.6}, "medium": {"2160": 9, "1440": 6, "1080": 3.5, "720": 1.8, "sd": 0.9},
		"high": {"2160": 15, "1440": 9, "1080": 6, "720": 3, "sd": 1.5}},
}

// Rough is where a picture starts to look rough (Mbps), by format and height: the rough-picture warning.
var Rough = map[string]map[string]float64{
	"hevc": {"2160": 6, "1440": 3.5, "1080": 2, "720": 1, "sd": 0.5},
	"h264": {"2160": 12, "1440": 6, "1080": 4, "720": 2, "sd": 1},
	"av1":  {"2160": 4.5, "1440": 2.5, "1080": 1.5, "720": 0.8, "sd": 0.4},
}

func ladderFormat(f string) string {
	if _, ok := Ladder[f]; ok {
		return f
	}
	return "hevc"
}

// HeightRow is the ladder row of a picture w x h.
func HeightRow(w, h int) string {
	switch {
	case h >= 2160 || w >= 3840:
		return "2160"
	case h >= 1440 || w >= 2560:
		return "1440"
	case h >= 1080 || w >= 1920:
		return "1080"
	case h >= 720 || w >= 1280:
		return "720"
	}
	return "sd"
}

// allowance is research's room for HDR or 10-bit (+25%) and 50-60 fps (+40%).
func allowance(hdr bool, fps float64) float64 {
	a := 1.0
	if hdr {
		a *= 1.25
	}
	if fps >= 48 {
		a *= 1.4
	}
	return a
}

// CapKbps is the level's cap for the new file (w x h after any scaling, format f): the ladder's number
// for its height (design E: the cap is the number the page shows; the HDR and frame-rate allowances
// raise the "looks rough" line, RoughWarning), never above the source's own video bitrate
// (sourceKbps; 0 unknown); 0 for "none".
func CapKbps(level, f string, w, h int, v *media.Stream, sourceKbps int) int {
	row, ok := Ladder[ladderFormat(f)][level]
	if !ok {
		return 0
	}
	kbps := int(math.Round(row[HeightRow(w, h)] * 1000))
	if sourceKbps > 0 && sourceKbps < kbps {
		kbps = sourceKbps // a file is never given more than it has now
	}
	return kbps
}

// OldBitrateLevel is the level a cap saved before S6 (Mbps) becomes: the nearest 1080p HEVC level
// (3/5/8), a tie going to the better picture. Values that are already levels stay.
func OldBitrateLevel(v string) string {
	switch v {
	case "40", "20", "10":
		return "high"
	case "6", "4":
		return "medium"
	}
	return v
}

// Shape is what the rough-picture warning needs of a video: its ladder row, HDR (or 10-bit), 50-60 fps.
type Shape struct {
	Row     string
	HDR     bool
	HighFPS bool
}

// ShapeOf is v's shape at its own size.
func ShapeOf(v *media.Stream) Shape {
	return Shape{Row: HeightRow(v.Width, v.Height), HDR: v.IsHDR() || v.BitDepth > 8, HighFPS: v.FrameRate >= 48}
}

var rowWords = map[string]string{"2160": "4K", "1440": "1440p", "1080": "1080p", "720": "720p", "sd": "SD"}
var betterLevel = map[string]string{"low": "Medium or No limit", "medium": "High or No limit", "high": "No limit"}

// RoughWarning is design E's rough-picture warning for a cap (level, format f) over the videos of the
// flow's libraries (shapes): the worst case where the cap is below research's "looks rough" line, after
// the HDR (+25%) and 50-60 fps (+40%) allowances; "" when none is.
func RoughWarning(level, f string, shapes []Shape) string {
	f = ladderFormat(f)
	row, ok := Ladder[f][level]
	if !ok {
		return ""
	}
	worst, by := Shape{}, 0.0
	for _, s := range shapes {
		fps := 0.0
		if s.HighFPS {
			fps = 60
		}
		if gap := Rough[f][s.Row]*allowance(s.HDR, fps) - row[s.Row]; gap > by {
			worst, by = s, gap
		}
	}
	if by <= 0 {
		return ""
	}
	// design's words (REDESIGN E, 03:44Z): "for <res> [HDR ]files[ at 50-60 fps] (<N> Mbps)"
	what, fps := rowWords[worst.Row], ""
	if worst.HDR {
		what += " HDR"
	}
	if worst.HighFPS {
		fps = " at 50-60 fps"
	}
	return fmt.Sprintf("%s may look blocky in dark or fast scenes for %s files%s (%g Mbps). Choose %s to avoid that.",
		upperFirst(level), what, fps, row[worst.Row], betterLevel[level])
}

// Mbps is kbps in the ladder's words: 3000 is "3", 2500 is "2.5".
func Mbps(kbps int) string { return strconv.FormatFloat(float64(kbps)/1000, 'f', -1, 64) }

// RowLabel is a ladder row in words ("sd": 480p and below).
func RowLabel(row string) string {
	switch row {
	case "2160":
		return "4K"
	case "sd":
		return "480p and below"
	}
	return row + "p"
}

// LadderRows are the ladder's rows, largest first.
var LadderRows = []string{"2160", "1440", "1080", "720", "sd"}

// VBRKbps is a VBR encode's target and limit (user review 11-12 item 1): the level's figure for the
// new file's height, never more than the limit, and neither more than the source has now.
func VBRKbps(level, f string, w, h int, v *media.Stream, sourceKbps, limit int) (target, max int) {
	target = CapKbps(level, f, w, h, v, sourceKbps)
	max = limit
	if sourceKbps > 0 && sourceKbps < max {
		max = sourceKbps
	}
	if target > max {
		target = max
	}
	return target, max
}
