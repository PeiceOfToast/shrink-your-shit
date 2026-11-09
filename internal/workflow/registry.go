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
)

// encodeSegment are the node types that run as one ffmpeg process (proposal, "Honest constraint").
var encodeSegment = []string{TypeSkips, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer}

// Definitions is the registry: built-in video nodes (plugins add theirs in Phase 4).
func Definitions() []StepDefinition {
	quality := enumOf(func() ([]any, []string) {
		var v []any
		var l []string
		for _, p := range rules.Slider {
			v, l = append(v, p.Position), append(l, p.Name)
		}
		return v, l
	})
	formats := enumOf(func() ([]any, []string) {
		var v []any
		var l []string
		for _, f := range []string{"hevc", "h264", "av1", "vp9"} {
			v, l = append(v, f), append(l, rules.VideoFormats[f].Name)
		}
		return v, l
	})
	formats["x-available"] = rules.Available() // the encoders this ffmpeg has
	audio := enumOf(func() ([]any, []string) {
		var v []any
		var l []string
		for _, a := range rules.AvailableAudio {
			v, l = append(v, a), append(l, rules.AudioWords[a])
		}
		return v, l
	})
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
				"default": []any{}, "description": "Folders, files or patterns in the library that are not looked at."}}, nil),
			SideEffects: "none"}),
		def(StepDefinition{Type: TypeSkips, Category: "filter", Name: "Leave some files alone", Icon: "filter", TypeLabel: "Filter",
			Description: "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{"includeDolbyVision": map[string]any{"type": "boolean", "default": false,
				"title": "Include Dolby Vision files"}}, nil)}),
		def(StepDefinition{Type: TypeConvert, Category: "video", Name: "Convert video", Icon: "video", TypeLabel: "Video",
			Description: "Makes the video smaller at the quality you choose.", Inputs: []Port{in},
			Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "writes_staging",
			Requires: []string{}, RequiresByConfig: map[string]map[string][]string{"format": {"hevc": {"encoder:libx265"},
				"h264": {"encoder:libx264"}, "av1": {"encoder:libsvtav1"}, "vp9": {"encoder:libvpx-vp9"}}},
			ConfigSchema: object(map[string]any{
				"quality":    withDefault(quality, 3, "Quality", "slider"),
				"format":     withDefault(formats, "hevc", "Video format", "segmented"),
				"resolution": withDefault(enum([]any{"keep", "1080", "720", "480"}, []string{"Same as the original", "At most 1080p", "At most 720p", "At most 480p"}), "keep", "Resolution", ""),
				"maxBitrate": withDefault(enum([]any{"none", "high", "medium", "low"}, []string{"No limit", "High", "Medium", "Low"}), "none", "Max bitrate", ""),
				"speed":      withDefault(enum([]any{"faster", "normal", "slower"}, []string{"Faster", "Normal", "Slower (smaller files)"}), "normal", "Speed", ""),
			}, []string{"quality", "format", "resolution", "maxBitrate", "speed"})}),
		def(StepDefinition{Type: TypeAudio, Category: "audio", Name: "Audio", Icon: "audio", TypeLabel: "Audio",
			Description: "Which sound tracks the new file has.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"audio":           withDefault(audio, "stereo_aac", "Audio", ""),
				"onlyMyLanguages": map[string]any{"type": "boolean", "default": true, "title": "Only my languages"},
			}, []string{"audio", "onlyMyLanguages"})}),
		def(StepDefinition{Type: TypeSubtitles, Category: "subtitles", Name: "Subtitles", Icon: "subtitles", TypeLabel: "Subtitles",
			Description: "Which subtitles the new file keeps.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"subtitles": withDefault(enum(anys(rules.AvailableSubtitles), []string{"Keep all", "Only my languages", "Text only", "As SRT files"}), "keep_all", "Subtitles", ""),
			}, []string{"subtitles"})}),
		def(StepDefinition{Type: TypeContainer, Category: "container", Name: "File type", Icon: "file", TypeLabel: "Container",
			Description: "The new file's type.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"fileType": withDefault(enum([]any{"mkv", "mp4", "webm", "keep"}, []string{"MKV", "MP4", "WebM", "Keep as is"}), "mkv", "File type", "segmented"),
			}, []string{"fileType"})}),
		def(StepDefinition{Type: TypeVerify, Category: "verify", Name: "Check the result", Icon: "check", TypeLabel: "Check",
			Description: "The new file must play through, look like the original and hold what you chose. If not, the original stays.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "none"}),
		def(StepDefinition{Type: TypeCommit, Category: "output", Name: "Save the result", Icon: "save", TypeLabel: "Output",
			Description: "Where the new file goes, and what happens to the original.", Inputs: []Port{in},
			Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "commits",
			ConfigSchema: object(map[string]any{
				"policy": withDefault(enum([]any{"separate_folder", "replace", "beside"},
					[]string{"Save to a separate folder", "Replace the original", "Put the new file next to the original"}), "separate_folder", "Save the result", ""),
				"folderMode": withDefault(enum([]any{"next_to_library", "one_folder"}, []string{"Next to each library", "One folder for all"}), "next_to_library", "Where", ""),
				"folder":     map[string]any{"type": "string", "title": "Folder", "x-ui": map[string]any{"widget": "folder"}},
			}, []string{"policy"})}),
		def(StepDefinition{Type: TypeLegacy, Category: "video", Name: "Earlier install's settings", Icon: "history", TypeLabel: "Video",
			Description: "The settings this workflow had before workflows existed. They run exactly as before; to change them, start from the Shrink videos steps.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, SideEffects: "commits", Deprecated: true,
			ConfigSchema: object(map[string]any{"template": map[string]any{"type": "object", "readOnly": true}}, []string{"template"})}),
	}
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
