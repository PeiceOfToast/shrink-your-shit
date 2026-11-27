// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"reflect"
	"slices"
	"testing"
)

// TestLadderEqualsTheEditorMock pins the whole table to ui/src/api/mockFlow.ts's LADDER, which the
// editor was built against (brief S2-6: "the server's numbers must equal the mock's"). The editor's
// quality test compares the served golden to the same mock, so changing a number on either side
// fails on both.
func TestLadderEqualsTheEditorMock(t *testing.T) {
	want := BitrateLadder{
		Unit: "Mbps",
		Rows: []LadderRow{
			{"2160", "2160p"},
			{"1440", "1440p"},
			{"1080", "1080p"},
			{"720", "720p"},
			{"sd", "480p (SD)"},
		},
		Formats: map[string]map[string]LadderLevels{
			"hevc": {
				"2160": {Low: 7.8, Medium: 12, High: 18},
				"1440": {Low: 3.6, Medium: 5.4, High: 7.8},
				"1080": {Low: 1.8, Medium: 3.6, High: 5.4},
				"720":  {Low: 1.5, Medium: 2.4, High: 3.6},
				"sd":   {Low: 0.6, Medium: 0.9, High: 1.5},
			},
			"h264": {
				"2160": {Low: 13, Medium: 20, High: 30},
				"1440": {Low: 6, Medium: 9, High: 13},
				"1080": {Low: 3, Medium: 6, High: 9},
				"720":  {Low: 2.5, Medium: 4, High: 6},
				"sd":   {Low: 1, Medium: 1.5, High: 2.5},
			},
			"av1": {
				"2160": {Low: 6.5, Medium: 10, High: 15},
				"1440": {Low: 3, Medium: 4.5, High: 6.5},
				"1080": {Low: 1.5, Medium: 3, High: 4.5},
				"720":  {Low: 1.25, Medium: 2, High: 3},
				"sd":   {Low: 0.5, Medium: 0.75, High: 1.25},
			},
		},
		VBRFormats:   []string{"hevc", "h264"},
		Factors:      LadderFactors{FPS60: 1.4, HDR: 1.25},
		VBRMaxFactor: 1.5,
		Slider: []LadderSlider{
			{1, "Smallest file", 27},
			{2, "Smaller", 25},
			{3, "Balanced", 23},
			{4, "Better quality", 21},
			{5, "Best quality", 19},
		},
		CRFOffset:     map[string]int{"hevc": 0, "h264": -5, "av1": 9},
		RowCRFShift:   map[string]int{"2160": 1, "1440": 0, "1080": 0, "720": -1, "sd": -1},
		ResolutionRow: map[string]string{"2160": "2160", "1080": "1080", "720": "720", "480": "sd"},
		CardOffsets:   map[string]map[string]int{"qsv": {"1": -2, "2": -2, "3": -1, "4": -1, "5": -1}},
	}
	if got := Ladder(); !reflect.DeepEqual(got, want) {
		t.Errorf("the ladder differs from the editor mock:\n got: %+v\nwant: %+v", got, want)
	}
}

// TestLadderCoversEveryFormatAndRow: a lookup the engine makes for any format and row it can pick
// has an answer, so a new format cannot be added to the step without its numbers.
func TestLadderCoversEveryFormatAndRow(t *testing.T) {
	l := Ladder()
	for format := range l.Formats {
		for _, row := range l.Rows {
			for _, level := range []string{"low", "medium", "high"} {
				if n, ok := l.LevelFor(format, row.Row, level); !ok || n <= 0 {
					t.Errorf("LevelFor(%q, %q, %q) = %v, %v; want a positive number", format, row.Row, level, n, ok)
				}
			}
		}
	}
	if _, ok := l.LevelFor("vp9", "1080", "medium"); ok {
		t.Error("LevelFor found numbers for vp9, which the ladder has none for")
	}
	if _, ok := l.LevelFor("hevc", "1080", "custom"); ok {
		t.Error("LevelFor answered for custom, which takes the typed numbers instead")
	}
}

// TestLadderVBRFormats: the formats the ladder offers variable bitrate for, and the rest.
func TestLadderVBRFormats(t *testing.T) {
	l := Ladder()
	for _, format := range []string{"hevc", "h264"} {
		if !l.VBR(format) {
			t.Errorf("VBR(%q) = false, want true", format)
		}
	}
	if l.VBR("av1") {
		t.Error("VBR(av1) = true, want false: AV1 takes constant bitrate or a CRF")
	}
	if slices.Contains(l.VBRFormats, "av1") {
		t.Error("vbrFormats unexpectedly lists av1")
	}
}
