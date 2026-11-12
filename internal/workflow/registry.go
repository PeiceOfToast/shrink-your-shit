// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The video node types (contract v2, "Node registry"): the Shrink videos preset's eight steps, in order,
// and the read-only node that carries an earlier install's settings.
const (
	TypeInput     = "input.library"
	TypeSkips     = "filter.video_skips"
	TypeConvert   = "video.convert"
	TypeAudio     = "audio.tracks"
	TypeSubtitles = "subtitles.tracks"
	TypeContainer = "container.output"
	TypeVerify    = "verify.output"
	TypeCommit    = "output.commit"
	TypeLegacy    = "video.legacy_settings"
	TypeCheck     = "filter.video" // V6a: "Check the file", a branch
	TypeEnd       = "flow.end"     // V6a: "Leave the file alone", an end with the person's words
)

// PresetOrder is the Shrink videos preset's chain (design SCREENS V3, steps 1-8).
var PresetOrder = []string{TypeInput, TypeSkips, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer, TypeVerify, TypeCommit}

// StepDefinition describes a node type for the palette, the inspector form and validation.
type StepDefinition struct {
	Type             string                         `json:"type"`
	TypeVersion      int                            `json:"typeVersion"`
	Category         string                         `json:"category"`
	FileTypes        []string                       `json:"fileTypes"`
	Name             string                         `json:"name"`
	Description      string                         `json:"description"`
	Icon             string                         `json:"icon"`
	TypeLabel        string                         `json:"typeLabel"`
	Inputs           []Port                         `json:"inputs"`
	Outputs          []Port                         `json:"outputs"`
	ConfigSchema     map[string]any                 `json:"configSchema"`
	AlwaysOn         bool                           `json:"alwaysOn"`
	Requires         []string                       `json:"requires"`
	RequiresByConfig map[string]map[string][]string `json:"requiresByConfig,omitempty"`
	RunsWith         []string                       `json:"runsWith,omitempty"`
	SideEffects      string                         `json:"sideEffects"`
	Source           map[string]string              `json:"source"`
	Deprecated       bool                           `json:"deprecated,omitempty"`
	// ReadOnly: made only by migration (an earlier install's settings): not offered in the palette, and
	// the UI shows it as a card "kept as it is", with no form (lead 02:26Z).
	ReadOnly bool `json:"readOnly"`
}

// Port is a named input or output.
type Port struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`    // file | control
	Outcome string `json:"outcome"` // success | error | skipped | branch
}

var (
	in      = Port{Name: "in", Label: "The file", Kind: "file", Outcome: "success"}
	success = Port{Name: "success", Label: "Next", Kind: "file", Outcome: "success"}
	skipped = Port{Name: "skipped", Label: "Left as it is", Kind: "control", Outcome: "skipped"}
	errPort = Port{Name: "error", Label: "Didn't work", Kind: "control", Outcome: "error"}
	yesPort = Port{Name: "match", Label: "Yes", Kind: "file", Outcome: "branch"} // V6a: a check's answers
	noPort  = Port{Name: "else", Label: "No", Kind: "file", Outcome: "branch"}
)

// encodeSegment are the node types that run as one ffmpeg process (proposal, "Honest constraint").
var encodeSegment = []string{TypeSkips, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer}

// Definitions is the registry: built-in video nodes (plugins add theirs in Phase 4).
func Definitions() []StepDefinition {
	// The words are design's (DESIGN/v2/mockups/workflow-stack.html, SCREENS V3, and the old editor's,
	// carried over): labels, a hint per choice (x-enumHints), and what this computer can do (x-available).
	picture := []string{"The smallest files. Fine detail can soften, mostly in dark or busy scenes.", "Small files, with a small loss of fine detail.",
		"Little visible change for most people. A good start.", "Keeps more detail. Files stay larger.", "Hard to tell from the original. Files stay largest."}
	var qv []any
	var ql []string
	for _, p := range rules.Slider {
		qv, ql = append(qv, p.Position), append(ql, p.Name)
	}
	quality := hinted(enum(qv, ql), picture)
	formats := hinted(enum([]any{"hevc", "h264", "av1", "vp9"}, []string{"HEVC (H.265)", "H.264", "AV1", "VP9"}), []string{
		"Small files that play on most TVs and phones made since 2016.", "Plays on everything, even old devices. Files are much larger than HEVC.",
		"The smallest files, but slow to make and only for newer TVs and devices.", "Small files for web browsers and Android. Few TVs play it."})
	formats["x-available"] = rules.Available() // the encoders this ffmpeg has
	audioValues := []string{"stereo_aac", "stereo_aac_surround", "eac3_surround", "ac3_surround", "stereo_opus", "as_is"}
	audioEncoder := map[string]string{"stereo_aac": "aac", "stereo_aac_surround": "aac", "eac3_surround": "eac3", "ac3_surround": "ac3", "stereo_opus": "libopus"}
	var audioAvailable []string
	for _, a := range audioValues {
		if e := audioEncoder[a]; e == "" || rules.HasEncoder(e) {
			audioAvailable = append(audioAvailable, a)
		}
	}
	audio := hinted(enum(anys(audioValues), []string{"Stereo AAC", "Stereo AAC, plus surround as it is", "Surround E-AC-3", "Surround AC-3", "Stereo Opus", "As it is"}), []string{
		"Plays on every TV and phone. Surround sound becomes stereo.", "Adds a stereo track for phones and TVs, and keeps the original surround track for sound systems.",
		"The best surround sound for modern TVs and sound bars.", "Surround sound that plays on almost every TV and sound bar; older than E-AC-3.",
		"The smallest audio, for MKV files. Doesn't play on Apple TV or most TVs.", "Tracks are copied unchanged."})
	audio["x-available"] = audioAvailable
	subtitles := hinted(enum(anys(rules.AvailableSubtitles), []string{"Keep all", "Keep only my languages", "Keep only text subtitles", "Save as separate .srt files"}), []string{
		"Every subtitle track is kept as it is.", "Keeps subtitles in your languages (Settings > General) and those with no language set; the others are left out, even if none match.",
		"Picture subtitles are left out. Text subtitles are kept.", "Text subtitles are saved next to the new file as .srt files instead of inside it; picture subtitles stay inside the file."})
	subtitles["description"] = "Subtitle pictures are never burned into the video."
	subtitles["x-available"] = []string{"keep_all", "my_languages", "text_only"}
	if rules.HasEncoder("srt") {
		subtitles["x-available"] = rules.AvailableSubtitles
	}
	builtin := map[string]string{"kind": "builtin"}
	def := func(d StepDefinition) StepDefinition {
		d.TypeVersion, d.FileTypes, d.Source = 1, []string{"video"}, builtin
		if d.Requires == nil {
			d.Requires = []string{}
		}
		if d.ConfigSchema == nil {
			d.ConfigSchema = object(nil, nil)
		}
		return d
	}
	return []StepDefinition{
		def(StepDefinition{Type: TypeInput, Category: "input", Name: "Library files", Icon: "folder", TypeLabel: "Input",
			Description: "Every video in the libraries that use this workflow.", Outputs: []Port{success}, AlwaysOn: true,
			ConfigSchema: object(map[string]any{"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"default": []any{}, "title": "Exclude patterns", "description": "Folders, files or patterns in the library that are left out."}}, nil),
			SideEffects: "none"}),
		def(StepDefinition{Type: TypeSkips, Category: "filter", Name: "Leave some files alone", Icon: "filter", TypeLabel: "Filter",
			Description: "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{"includeDolbyVision": map[string]any{"type": "boolean", "default": false,
				"title": "Include Dolby Vision files", "description": "Off: Dolby Vision files are left as they are. On: they become HDR10 and the Dolby Vision layer is removed. Some Dolby Vision files (profile 5) are always left alone, because converting them would show wrong colours."}}, nil)}),
		def(StepDefinition{Type: TypeConvert, Category: "video", Name: "Convert video", Icon: "video", TypeLabel: "Video",
			Description: "Makes the video smaller at the quality you choose.", Inputs: []Port{in},
			Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "writes_staging",
			Requires: []string{}, RequiresByConfig: map[string]map[string][]string{"format": {"hevc": {"encoder:libx265"},
				"h264": {"encoder:libx264"}, "av1": {"encoder:libsvtav1"}, "vp9": {"encoder:libvpx-vp9"}}},
			ConfigSchema: object(map[string]any{
				"quality": withDefault(quality, 3, "Quality", "slider"),
				"format":  withDefault(formats, "hevc", "Video format", "segmented"),
				"resolution": described(withDefault(enum([]any{"keep", "1080", "720", "480"}, []string{"Keep as is", "At most 1080p", "At most 720p", "At most 480p"}), "keep", "Resolution", ""),
					"Videos are only ever made smaller, never stretched."),
				"maxBitrate": bitrate(described(withDefault(enum([]any{"none", "high", "medium", "low"}, []string{"No limit", "High", "Medium", "Low"}), "none", "Max bitrate", ""),
					"Caps how much data each second of video may use. A file never gets more than it has now. Quality still sets the picture; the cap only matters in busy scenes.")),
				"speed": described(withDefault(enum([]any{"faster", "normal", "slower"}, []string{"Faster, larger files", "Normal", "Slower, smaller files"}), "normal", "Speed", ""),
					"Slower takes about twice as long."),
				// user review 11-12 item 1: VBR aims at the level's bitrate for the new file's height, under a limit
				"rateControl": described(withDefault(enum([]any{"quality", "vbr"}, []string{"Quality", "VBR"}), "quality", "Rate control", "segmented"),
					"Quality keeps the picture steady and uses the level as a cap. VBR aims at the level's bitrate and never goes above your limit."),
				"vbrMaxKbps": described(map[string]any{"type": "integer", "minimum": 100, "maximum": 500000, "title": "VBR limit (kbps)"},
					"The most data each second may use with VBR. A file never gets more than it has now."),
			}, []string{"quality", "format", "resolution", "maxBitrate", "speed"})}),
		def(StepDefinition{Type: TypeAudio, Category: "audio", Name: "Audio", Icon: "audio", TypeLabel: "Audio",
			Description: "Which sound tracks the new file has.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"audio": withDefault(audio, "stereo_aac", "Audio", ""),
				"onlyMyLanguages": map[string]any{"type": "boolean", "default": true, "title": "Only my languages",
					// design's words (02:38Z), true to planAudio: with none matching, a stereo choice keeps the main track only
					"description": "Keeps only tracks in your languages (Settings > General). Tracks with no language set are always kept. If none match, a stereo choice keeps the main track; the other choices keep all tracks."},
			}, []string{"audio", "onlyMyLanguages"})}),
		def(StepDefinition{Type: TypeSubtitles, Category: "subtitles", Name: "Subtitles", Icon: "subtitles", TypeLabel: "Subtitles",
			Description: "Which subtitles the new file keeps.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"subtitles": withDefault(subtitles, "keep_all", "Subtitles", ""),
			}, []string{"subtitles"})}),
		def(StepDefinition{Type: TypeContainer, Category: "container", Name: "File type", Icon: "file", TypeLabel: "Container",
			Description: "The new file's type.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"fileType": withDefault(hinted(enum([]any{"mkv", "mp4", "webm", "keep"}, []string{"MKV", "MP4", "WebM", "Keep as is"}), []string{
					"Holds every kind of track; the usual choice for Plex and Jellyfin.",
					"Best for phones, browsers and Apple TV. Files with picture subtitles are left alone (the reason is in Jobs) unless the Subtitles step leaves those out. Styled subtitles become plain text. Surround formats MP4 can't hold become E-AC-3.",
					"For web browsers. Only with AV1 or VP9 video and Opus audio.",
					"Each file keeps its current type. A file whose type can't hold the chosen video or audio becomes MKV."}), "mkv", "File type", "segmented"),
			}, []string{"fileType"})}),
		def(StepDefinition{Type: TypeVerify, Category: "verify", Name: "Check the result", Icon: "check", TypeLabel: "Check",
			Description: "The new file must play through, look like the original and hold what you chose. If not, the original stays.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "none"}),
		def(StepDefinition{Type: TypeCommit, Category: "output", Name: "Save the result", Icon: "save", TypeLabel: "Output",
			Description: "Where the new file goes, and what happens to the original.", Inputs: []Port{in},
			Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "commits",
			ConfigSchema: object(map[string]any{
				"policy": withDefault(hinted(enum([]any{"separate_folder", "replace", "beside"},
					[]string{"Save to a separate folder", "Replace the original", "Put the new file next to the original"}), []string{
					"New files go to the folder below, in the same sub-folders. Your originals aren't changed, so no space is freed until you remove them yourself.",
					"The original is kept for 7 days (Settings > Safety), then deleted. This frees space. You can get an original back from its job.",
					"Both stay. Nothing is deleted."}), "separate_folder", "Save the result", ""),
				"folderMode": described(withDefault(hinted(enum([]any{"next_to_library", "one_folder"}, []string{"Next to each library", "One folder for all"}), []string{
					"Each library gets a folder beside it, e.g. /media/movies (shrunk). Same disk, and it works when one workflow serves several libraries.",
					"Every converted file goes into the folder below, in the same sub-folders as in its library."}), "next_to_library", "Where new files go", ""),
					"Pick a folder next to each library, or one folder for all of them. Your originals stay where they are."),
				"folder": map[string]any{"type": "string", "title": "Folder", "description": "New files go to this folder, in the same sub-folders.",
					"x-placeholder": "/media/movies (shrunk)", "x-ui": map[string]any{"widget": "folder"}},
			}, []string{"policy"})}),
		def(StepDefinition{Type: TypeLegacy, Category: "video", Name: "Earlier install's settings", Icon: "history", TypeLabel: "Video",
			Description: "The settings this workflow had before workflows existed. They run exactly as before; to change them, start from the Shrink videos steps.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, SideEffects: "commits", Deprecated: true, ReadOnly: true,
			ConfigSchema: object(nil, nil)}), // its config (the template) is kept as it is: no form
		// V6a: branches. Each output port has its own words (ui-shop 02:05Z): the UI never makes them up.
		def(StepDefinition{Type: TypeCheck, Category: "filter", Name: "Check the file", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by what it is. Every condition you set must hold for Yes.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"codec": described(map[string]any{"type": "array", "title": "Video format is one of", "items": enum([]any{"hevc", "h264", "av1", "vp9", "mpeg4", "mpeg2video", "vc1", "wmv3"},
					[]string{"HEVC", "H.264", "AV1", "VP9", "MPEG-4", "MPEG-2", "VC-1", "WMV"})}, "Leave empty for any."),
				"minBitrateKbps": described(map[string]any{"type": "integer", "minimum": 0, "title": "Bitrate is at least (kbps)"}, "0 for any."),
				"resolution":     withDefault(enum([]any{0, 720, 1080, 2160}, []string{"Any", "At least 720p", "At least 1080p", "4K (2160p)"}), 0, "Resolution", ""),
				"hdr":            withDefault(enum([]any{"", "yes", "no"}, []string{"Either", "HDR", "Not HDR"}), "", "HDR", "segmented"),
				"dolbyVision":    withDefault(enum([]any{"", "yes", "no"}, []string{"Either", "Dolby Vision", "Not Dolby Vision"}), "", "Dolby Vision", "segmented"),
				"fps":            described(map[string]any{"type": "number", "minimum": 0, "maximum": 1000, "title": "Frame rate is at least (fps)"}, "0 for any."),
				"size":           described(map[string]any{"type": "integer", "minimum": 0, "title": "File is at least (MB)"}, "0 for any."),
				"container": described(map[string]any{"type": "array", "title": "File type is one of", "items": enum([]any{"mkv", "mp4", "avi", "mov", "webm", "ts", "m2ts", "wmv", "flv", "mpg"},
					[]string{"MKV", "MP4", "AVI", "MOV", "WebM", "TS", "M2TS", "WMV", "FLV", "MPG"})}, "Leave empty for any."),
			}, nil)}),
		def(StepDefinition{Type: TypeEnd, Category: "flow", Name: "Leave the file alone", Icon: "stop", TypeLabel: "End",
			Description: "The file is left as it is, and the reason below shows in its history.",
			Inputs:      []Port{in}, Outputs: []Port{}, SideEffects: "none",
			ConfigSchema: object(map[string]any{"reason": withDefault(map[string]any{"type": "string", "maxLength": 200}, "Left alone by this workflow.", "Reason shown", "")}, nil)}),
	}
}

// hinted adds a hint per choice (shown when it is chosen), beside x-enumLabels.
func hinted(s map[string]any, hints []string) map[string]any {
	s["x-enumHints"] = hints
	return s
}

func described(s map[string]any, d string) map[string]any {
	s["description"] = d
	return s
}

// bitrate adds research's ladder (format → level → row → Mbps), so the page shows "Medium · 5 Mbps at
// 1080p" for the chosen format and the new file's height.
func bitrate(s map[string]any) map[string]any {
	ladder := map[string]any{"vp9": rules.Ladder["hevc"]} // VP9 uses HEVC's numbers, as S6's setting does (review 8760065)
	for f, row := range rules.Ladder {
		ladder[f] = row
	}
	s["x-bitrateLadder"] = ladder
	return s
}

// Definition returns the registry entry for a node type.
func Definition(typ string) (StepDefinition, bool) {
	i := slices.IndexFunc(Definitions(), func(d StepDefinition) bool { return d.Type == typ })
	if i < 0 {
		return StepDefinition{}, false
	}
	return Definitions()[i], true
}

func object(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": true}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func enum(values []any, labels []string) map[string]any {
	return map[string]any{"enum": values, "x-enumLabels": labels}
}

func enumOf(f func() ([]any, []string)) map[string]any { return enum(f()) }

func withDefault(s map[string]any, def any, title, widget string) map[string]any {
	out := map[string]any{"default": def, "title": title}
	for k, v := range s {
		out[k] = v
	}
	if widget != "" {
		out["x-ui"] = map[string]any{"widget": widget}
	}
	return out
}

func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
