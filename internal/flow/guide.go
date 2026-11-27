// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

// The guided page's fixed table (page 06, "Data and API"). Stage 1 has one starter and one guide; a
// later stage builds both from the step definitions. The shapes are ui/src/api/flowTypes.ts's
// GuideDoc, and the values are the ones the page was drawn against.

// Answers is one guided answers object: the keys the guide's fields use.
type Answers map[string]any

// GuideDoc is what the guided page asks: its chips, its parts and fields, the defaults a new
// workflow starts from, and the languages the app offers for its language fields.
type GuideDoc struct {
	Chips     []GuideChip `json:"chips"`
	Parts     []GuidePart `json:"parts"`
	Defaults  Answers     `json:"defaults"`
	Languages []string    `json:"languages"`
}

// GuideChip is one one-tap answer the page shows above the questions. An always chip cannot be
// turned off: the app holds to it whatever the person answers.
type GuideChip struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Always    bool    `json:"always"`
	On        Answers `json:"on"`
	Off       Answers `json:"off"`
	Available bool    `json:"available"`
	Why       string  `json:"why,omitempty"`
}

// GuidePart is one group of questions, in the page's order.
type GuidePart struct {
	ID     string       `json:"id"`
	Label  string       `json:"label"`
	Fields []GuideField `json:"fields"`
}

// GuideField is one question. Type is choice, number, yesno, languages or folder; a choice field
// has its choices. More puts a rare setting under "More".
type GuideField struct {
	Key     string        `json:"key"`
	Label   string        `json:"label"`
	Type    string        `json:"type"`
	More    bool          `json:"more,omitempty"`
	Choices []GuideChoice `json:"choices,omitempty"`
}

// GuideChoice is one answer of a choice field. Available false greys it; Why says why.
type GuideChoice struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Why       string `json:"why,omitempty"`
}

// DefaultAnswers is what a new workflow starts from: the page's sensible defaults, ticked.
func DefaultAnswers() Answers {
	// The audio default is as_is keeping every track, with the languages left unset: converting the
	// audio is item 7, so stereo AAC with the person's languages would make the default workflow
	// unpublishable today. Back to stereo_aac and the default languages with item 7 (ui-shop ask,
	// 11-27 20:14Z); the other audio choices are offered greyed until then.
	return Answers{
		"quality":             3,
		"slower":              false,
		"format":              "hevc",
		"resolution":          "keep",
		"useGpu":              true,
		"ifGpuFails":          "cpu",
		"audio":               "as_is",
		"allLanguages":        true,
		"languages":           []string{},
		"languagesEverywhere": false,
		"subtitles":           "keep_all",
		"subtitleLanguages":   []string{},
		"fileType":            "mkv",
		"includeDolbyVision":  false,
		"original":            "next_to_original",
		"folder":              "",
	}
}

// Guide is the guided page's table as the API serves it.
func Guide() GuideDoc {
	return GuideDoc{
		Chips: []GuideChip{
			{ID: "shrink_hevc", Label: "Shrink to HEVC", On: Answers{"format": "hevc"}, Off: Answers{"format": "h264"}, Available: true},
			{ID: "keep_my_languages", Label: "Keep only my languages", On: Answers{"allLanguages": false}, Off: Answers{"allLanguages": true}, Available: true},
			{ID: "use_gpu", Label: "Use the graphics card (QSV)", On: Answers{"useGpu": true}, Off: Answers{"useGpu": false}, Available: true},
			{ID: "never_worse", Label: "Never make anything worse", Always: true, On: Answers{}, Off: Answers{}, Available: true},
			{ID: "hdr_kept", Label: "HDR kept, Dolby Vision left alone", Always: true, On: Answers{}, Off: Answers{}, Available: true},
		},
		Parts: []GuidePart{
			{
				ID: "video", Label: "Video",
				Fields: []GuideField{
					{Key: "format", Label: "Format", Type: "choice", Choices: []GuideChoice{
						{Value: "hevc", Label: "HEVC", Available: true},
						{Value: "h264", Label: "H.264", Available: true},
						{Value: "av1", Label: "AV1", Available: true},
					}},
					{Key: "quality", Label: "Quality", Type: "number"},
					{Key: "useGpu", Label: "Use the graphics card", Type: "yesno", More: true},
					{Key: "ifGpuFails", Label: "If the graphics card can't do it", Type: "choice", More: true, Choices: []GuideChoice{
						{Value: "cpu", Label: "Use the processor", Available: true},
						{Value: "stop", Label: "Stop and tell me", Available: true},
					}},
					{Key: "resolution", Label: "Picture size", Type: "choice", Choices: []GuideChoice{
						{Value: "keep", Label: "Same as the source", Available: true},
						{Value: "1080", Label: "1080p", Available: true},
						{Value: "720", Label: "720p", Available: true},
						{Value: "480", Label: "480p", Available: true},
					}},
				},
			},
			{
				ID: "audio", Label: "Audio",
				Fields: []GuideField{
					{Key: "audio", Label: "Audio", Type: "choice", Choices: []GuideChoice{
						{Value: "stereo_aac", Label: "Stereo AAC", Available: false, Why: AudioWhy},
						{Value: "stereo_aac_surround", Label: "AAC, same channels as the source", Available: false, Why: AudioWhy},
						{Value: "eac3_surround", Label: "E-AC-3, surround kept", Available: false, Why: AudioWhy},
						{Value: "ac3_surround", Label: "AC-3, surround kept", Available: false, Why: AudioWhy},
						{Value: "stereo_opus", Label: "Stereo Opus", Available: false, Why: AudioWhy},
						{Value: "as_is", Label: "Leave as it is", Available: true},
					}},
					{Key: "languages", Label: "Keep these languages", Type: "languages"},
				},
			},
			{
				ID: "subtitles", Label: "Subtitles",
				Fields: []GuideField{
					{Key: "subtitles", Label: "Subtitles", Type: "choice", Choices: []GuideChoice{
						{Value: "keep_all", Label: "Keep all", Available: true},
						{Value: "my_languages", Label: "Keep my languages", Available: true},
						{Value: "text_only", Label: "Text only", Available: true},
						{Value: "srt", Label: "Make SRT files", Available: true},
					}},
					{Key: "subtitleLanguages", Label: "Subtitle languages", Type: "languages"},
				},
			},
			{
				ID: "file_type", Label: "File type",
				Fields: []GuideField{
					{Key: "fileType", Label: "File type", Type: "choice", Choices: []GuideChoice{
						{Value: "mkv", Label: "MKV", Available: true},
						{Value: "mp4", Label: "MP4", Available: true},
						{Value: "webm", Label: "WebM", Available: true},
						{Value: "keep", Label: "Keep the same", Available: true},
					}},
				},
			},
			{
				ID: "original", Label: "The original",
				Fields: []GuideField{
					{Key: "original", Label: "Where the new file goes", Type: "choice", Choices: []GuideChoice{
						{Value: "replace", Label: "Replace the original", Available: false, Why: ReplaceWhy},
						{Value: "next_to_original", Label: "Next to the original", Available: true},
						{Value: "one_folder", Label: "A different folder", Available: true},
						{Value: "beside_library", Label: "A folder beside the library", Available: true},
					}},
					{Key: "folder", Label: "Folder", Type: "folder"},
				},
			},
		},
		Defaults:  DefaultAnswers(),
		Languages: []string{"eng", "ger"},
	}
}

// Language is one of the languages the app offers for its language fields.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Languages is the app's language table: three-letter bibliographic codes (ISO 639-2/B) and their
// English names, as the guided page's language pickers list them.
func Languages() []Language {
	return []Language{
		{"eng", "English"},
		{"ger", "German"},
		{"fre", "French"},
		{"spa", "Spanish"},
		{"ita", "Italian"},
		{"por", "Portuguese"},
		{"dut", "Dutch"},
		{"swe", "Swedish"},
		{"nor", "Norwegian"},
		{"dan", "Danish"},
		{"fin", "Finnish"},
		{"ice", "Icelandic"},
		{"pol", "Polish"},
		{"cze", "Czech"},
		{"slo", "Slovak"},
		{"hun", "Hungarian"},
		{"rum", "Romanian"},
		{"bul", "Bulgarian"},
		{"gre", "Greek"},
		{"tur", "Turkish"},
		{"rus", "Russian"},
		{"ukr", "Ukrainian"},
		{"srp", "Serbian"},
		{"hrv", "Croatian"},
		{"bos", "Bosnian"},
		{"slv", "Slovenian"},
		{"mac", "Macedonian"},
		{"alb", "Albanian"},
		{"est", "Estonian"},
		{"lav", "Latvian"},
		{"lit", "Lithuanian"},
		{"cat", "Catalan"},
		{"baq", "Basque"},
		{"glg", "Galician"},
		{"wel", "Welsh"},
		{"gle", "Irish"},
		{"heb", "Hebrew"},
		{"ara", "Arabic"},
		{"per", "Persian"},
		{"hin", "Hindi"},
		{"urd", "Urdu"},
		{"ben", "Bengali"},
		{"tam", "Tamil"},
		{"tel", "Telugu"},
		{"tha", "Thai"},
		{"vie", "Vietnamese"},
		{"ind", "Indonesian"},
		{"may", "Malay"},
		{"fil", "Filipino"},
		{"chi", "Chinese"},
		{"jpn", "Japanese"},
		{"kor", "Korean"},
		{"afr", "Afrikaans"},
		{"swa", "Swahili"},
	}
}
