// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

// The bitrate ladder is page 07's one recommended-numbers table: the Mb/s a level asks for at each
// output size, format and level, plus the Quality slider's numbers and the offsets that turn them
// into a file's CRF. The editor reads it from GET /api/v2/bitrate-ladder and the engine reads the
// same value, so the page and the encode cannot disagree (page 07, "Recommended numbers").
//
// Numbers are at 30 fps SDR, for live-action film and television. They are starting values to
// calibrate, like the slider's; they match the editor's mock (ui/src/api/mockFlow.ts LADDER),
// which the page was built against.

// BitrateLadder is the whole table, in the shape the editor's BitrateLadder type reads.
type BitrateLadder struct {
	Unit string `json:"unit"`
	// Rows are the picture sizes, largest first.
	Rows []LadderRow `json:"rows"`
	// Formats is formats[format][row] = the three levels.
	Formats map[string]map[string]LadderLevels `json:"formats"`
	// VBRFormats are the formats that take variable bitrate; the others are constant only.
	VBRFormats []string `json:"vbrFormats"`
	// Factors raise a level's number for fast and HDR pictures.
	Factors LadderFactors `json:"factors"`
	// VBRMaxFactor is the at-most a level fills for VBR: this many times the average.
	VBRMaxFactor float64 `json:"vbrMaxFactor"`
	// Slider is the Quality slider: the processor's CRF at 1080p for each position.
	Slider []LadderSlider `json:"slider"`
	// CRFOffset is added to the slider's number for each format.
	CRFOffset map[string]int `json:"crfOffset"`
	// RowCRFShift moves the slider's number by the picture size's row.
	RowCRFShift map[string]int `json:"rowCrfShift"`
	// ResolutionRow maps a size choice to its ladder row (480 is the sd row).
	ResolutionRow map[string]string `json:"resolutionRow"`
	// CardOffsets are the card families' CRF offsets, by slider position, for HEVC on a card only.
	CardOffsets map[string]map[string]int `json:"cardOffsets"`
}

// LadderRow is one picture-size row and its label.
type LadderRow struct {
	Row   string `json:"row"`
	Label string `json:"label"`
}

// LadderLevels is the three recommended numbers for one format at one row.
type LadderLevels struct {
	Low    float64 `json:"low"`
	Medium float64 `json:"medium"`
	High   float64 `json:"high"`
}

// LadderFactors raise a level's number; the key is the field's own name in the contract.
type LadderFactors struct {
	FPS60 float64 `json:"fps60"`
	HDR   float64 `json:"hdr"`
}

// LadderSlider is one position of the Quality slider.
type LadderSlider struct {
	Position int    `json:"position"`
	Name     string `json:"name"`
	CRF      int    `json:"crf"`
}

// ladder is the table itself. Its numbers are the editor mock's, number for number (a Go table
// test and the editor's test both compare against them), and differ from
// RESEARCH/bitrate-ladder.md's first draft, which was context to calibrate rather than a contract.
var ladder = BitrateLadder{
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
	CRFOffset: map[string]int{"hevc": 0, "h264": -5, "av1": 9},
	RowCRFShift: map[string]int{
		"2160": 1, "1440": 0, "1080": 0, "720": -1, "sd": -1,
	},
	ResolutionRow: map[string]string{"2160": "2160", "1080": "1080", "720": "720", "480": "sd"},
	CardOffsets:   map[string]map[string]int{"qsv": {"1": -2, "2": -2, "3": -1, "4": -1, "5": -1}},
}

// Ladder returns page 07's recommended numbers. Callers must not change the value they get back:
// the maps and slices are shared with every other reader.
func Ladder() BitrateLadder { return ladder }

// LevelFor is the recommended number for one format, ladder row and level ("low", "medium" or
// "high"), and whether the table holds one.
func (l BitrateLadder) LevelFor(format, row, level string) (float64, bool) {
	levels, ok := l.Formats[format][row]
	if !ok {
		return 0, false
	}
	switch level {
	case "low":
		return levels.Low, true
	case "medium":
		return levels.Medium, true
	case "high":
		return levels.High, true
	}
	return 0, false
}

// VBR reports whether the format takes variable bitrate.
func (l BitrateLadder) VBR(format string) bool {
	for _, f := range l.VBRFormats {
		if f == format {
			return true
		}
	}
	return false
}
