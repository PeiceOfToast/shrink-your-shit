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
	// FB1b (docs/architecture/flow-builder.md section 2): the first questions, actions and ends.
	TypeCheckName      = "check.name"          // the file's name or path matches a pattern
	TypeCheckAge       = "check.age"           // the original was last changed N days ago
	TypeCheckVariable  = "check.variable"      // a {{name}} is set / equals / contains / more / less
	TypeCheckLibrary   = "check.library"       // the file comes from one of the chosen libraries
	TypeCheckHardLinks = "check.hardlinks"     // the original shares its data with another name
	TypeSetVariable    = "action.set_variable" // sets a {{name}} for the steps after it
	TypeNote           = "action.note"         // writes a line into the job log
	TypeFail           = "flow.fail"           // ends the run as failed, the original untouched
	// FB2a (section 2.6): actions on the saved result.
	TypeMediaServer = "action.media_server" // asks the connected media server(s) to look at the folder
	TypeDownloader  = "action.downloader"   // asks the connected downloader(s) to re-read the file
	TypeNotify      = "action.notify"       // sends a message to a notification target
	TypeSidecar     = "action.sidecar"      // writes a text file next to the new file
	TypeChecksum    = "action.checksum"     // computes a checksum of the new file
	TypeCopy        = "action.copy"         // copies the new file to another folder
	// FB2b
	TypeCommand          = "action.command"           // runs a program on the saved result; behind the Safety switch
	TypeLookupDownloader = "action.lookup_downloader" // asks the connected downloader what this file is
	// FB2c: tags on the job record (user decision 2: the word is "tag")
	TypeCheckTag = "check.tag"  // the file's record carries any / all / none of the chosen tags
	TypeTag      = "action.tag" // adds or removes tags on the file's record
	// FB4: the remaining flow cards
	TypeCheckRandom = "check.random" // a chosen share of files answers Yes
	TypeCheckResult = "check.result" // how the new file compares with the original: after the check (FB4b-3) or after the save (FB4b)
	TypeWait        = "action.wait"  // pauses the run for a while, or puts the file back in the queue until a time of day
	TypeRequeue     = "flow.requeue" // the file goes back to the queue for later, at most so many times
	TypeGoto        = "flow.goto"    // the file continues in another published workflow (FB4b)
)

// Questions are the step types that answer Yes or No (RunsQuestion); Actions the ones that do one thing
// before the conversion (RunsAction); Ends the ones that end a run.
var (
	Questions = []string{TypeCheck, TypeCheckName, TypeCheckAge, TypeCheckVariable, TypeCheckLibrary, TypeCheckHardLinks, TypeCheckTag, TypeCheckRandom, TypeCheckResult}
	Actions   = []string{TypeSetVariable, TypeNote, TypeTag, TypeWait, TypeLookupDownloader, TypeMediaServer, TypeDownloader, TypeNotify, TypeSidecar, TypeChecksum, TypeCopy, TypeCommand}
	Ends      = []string{TypeEnd, TypeFail, TypeRequeue, TypeGoto}
)

// NotYet are the cards this build offers for drafts but can't run yet (Available false): Publish refuses
// a workflow that uses one. Empty since FB1c, which made the worker run every card the registry offers;
// a later slice that adds a card before its engine half lists it here.
var NotYet = map[string]bool{}

// IsQuestion, IsAction and IsEnd say what kind of step a type is.
func IsQuestion(typ string) bool { return slices.Contains(Questions, typ) }
func IsAction(typ string) bool   { return slices.Contains(Actions, typ) }
func IsEnd(typ string) bool      { return slices.Contains(Ends, typ) }

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
	// The card contract (docs/architecture/flow-builder.md, section 1): what the step does, where it may sit
	// in a run, what it publishes and needs, what state the file is in on each output, and whether this
	// build runs it. Both editors and the run engine read these; validation's phase rules build on them.
	RunsAs    string            `json:"runsAs"`    // RunsInput | RunsQuestion | RunsAction | RunsPlan | RunsVerify | RunsCommit | RunsEnd
	Phase     []string          `json:"phase"`     // the phases it may sit in: PhaseBefore ... PhaseAfter
	Publishes []Published       `json:"publishes"` // the {{name}} variables it sets for later steps
	Reads     []string          `json:"reads"`     // the {{name}} variables it must have
	FileOn    map[string]string `json:"fileOn"`    // per output port: FileOriginal | FileStaged | FileSaved | FileNone
	Available bool              `json:"available"` // this build runs it; false: it can be drafted, not published
}

// Published is a variable a step sets for the steps after it.
type Published struct {
	Name  string `json:"name"`  // a NamePattern name, never a built-in
	Type  string `json:"type"`  // string | number | bool | list
	Words string `json:"words"` // what it holds, for the variable picker
}

// The kinds of step (RunsAs).
const (
	RunsInput    = "input"    // where files come from
	RunsQuestion = "question" // answers Yes or No; changes nothing
	RunsAction   = "action"   // does one thing: a variable, a note, a name, a folder, a message to another program
	RunsPlan     = "plan"     // shapes the one conversion
	RunsVerify   = "verify"   // checks the result against the original
	RunsCommit   = "commit"   // saves the result and decides the original's fate
	RunsEnd      = "end"      // ends the run with a verdict
)

// The phases of a run, in order (Phase).
const (
	PhaseBefore  = "before"  // on the original: questions and actions that change no file
	PhaseConvert = "convert" // the one conversion: plan steps, contiguous
	PhaseCheck   = "check"   // the result checked against the original
	PhaseSave    = "save"    // the result saved
	PhaseAfter   = "after"   // on the saved result: actions and messages; a failure never undoes the save
)

// Phases are the phases in run order.
var Phases = []string{PhaseBefore, PhaseConvert, PhaseCheck, PhaseSave, PhaseAfter}

// What state the file is in on an output port (FileOn).
const (
	FileOriginal = "original" // the original, untouched
	FileStaged   = "staged"   // a new file in the staging folder; the original untouched
	FileSaved    = "saved"    // the new file in place; the original's fate decided
	FileNone     = "none"     // no file goes this way (an end's words, a control port)
)

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
		"Picture subtitles are left out. Text subtitles are kept.", "Subtitles in your languages (Settings > General) are saved next to the new file as .srt files; other languages, and picture subtitles, stay inside the file."})
	subtitles["description"] = "Subtitle pictures are never burned into the video."
	subtitles["x-available"] = []string{"keep_all", "my_languages", "text_only"}
	if rules.HasEncoder("srt") {
		subtitles["x-available"] = rules.AvailableSubtitles
	}
	builtin := map[string]string{"kind": "builtin"}
	def := func(d StepDefinition) StepDefinition {
		d.TypeVersion, d.FileTypes, d.Source, d.Available = 1, []string{"video"}, builtin, !NotYet[d.Type]
		if d.Requires == nil {
			d.Requires = []string{}
		}
		if d.ConfigSchema == nil {
			d.ConfigSchema = object(nil, nil)
		}
		if d.Publishes == nil {
			d.Publishes = []Published{}
		}
		if d.Reads == nil {
			d.Reads = []string{}
		}
		if d.Phase == nil {
			d.Phase = phaseOf(d.RunsAs)
		}
		if d.FileOn == nil {
			d.FileOn = fileOnOf(d)
		}
		return d
	}
	return []StepDefinition{
		def(StepDefinition{Type: TypeInput, RunsAs: RunsInput, Category: "input", Name: "Library files", Icon: "folder", TypeLabel: "Input",
			Description: "Every video in the libraries that use this workflow.", Outputs: []Port{success}, AlwaysOn: true,
			ConfigSchema: object(map[string]any{"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"default": []any{}, "title": "Exclude patterns", "description": "Folders, files or patterns in the library that are left out."}}, nil),
			SideEffects: "none"}),
		def(StepDefinition{Type: TypeSkips, RunsAs: RunsPlan, Category: "filter", Name: "Leave some files alone", Icon: "filter", TypeLabel: "Filter",
			Description: "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{"includeDolbyVision": typedRef(map[string]any{"type": "boolean", "default": false,
				"title": "Include Dolby Vision files", "description": "Off: Dolby Vision files are left as they are. On: they become HDR10 and the Dolby Vision layer is removed. Some Dolby Vision files (profile 5) are always left alone, because converting them would show wrong colours."})}, nil)}),
		def(StepDefinition{Type: TypeConvert, RunsAs: RunsPlan, Category: "video", Name: "Convert video", Icon: "video", TypeLabel: "Video",
			Description: "Makes the video smaller at the quality you choose.", Inputs: []Port{in},
			Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, RunsWith: encodeSegment, SideEffects: "writes_staging",
			Requires: []string{}, RequiresByConfig: map[string]map[string][]string{"format": {"hevc": {"encoder:libx265"},
				"h264": {"encoder:libx264"}, "av1": {"encoder:libsvtav1"}, "vp9": {"encoder:libvpx-vp9"}}},
			ConfigSchema: object(map[string]any{
				// the slider and More options write this; forms skip readOnly settings, so it is not a field
				"kind": withDefault(map[string]any{"enum": []any{"slider", "selectors"}, "x-enumLabels": []string{"Slider", "Custom settings"},
					"readOnly": true}, "slider", "Kind", ""),
				"quality": typedRef(withDefault(quality, 3, "Quality", "slider")),
				"format":  typedRef(withDefault(formats, "hevc", "Video format", "segmented")),
				"resolution": typedRef(described(withDefault(enum([]any{"keep", "1080", "720", "480"}, []string{"Keep as is", "At most 1080p", "At most 720p", "At most 480p"}), "keep", "Resolution", ""),
					"Videos are only ever made smaller, never stretched.")),
				"maxBitrate": typedRef(bitrate(described(withDefault(enum([]any{"none", "high", "medium", "low"}, []string{"No limit", "High", "Medium", "Low"}), "none", "Bitrate level", ""),
					"Caps how much data each second of video may use. A file never gets more than it has now. Quality still sets the picture; the cap only matters in busy scenes."))),
				"speed": typedRef(described(withDefault(enum([]any{"faster", "normal", "slower"}, []string{"Faster, larger files", "Normal", "Slower, smaller files"}), "normal", "Speed", ""),
					"Slower takes about twice as long.")),
				// user review 11-13 item A: a level is the average (its peak 1.5x); VBR is CRF 23 under the max, no level
				// the two *_legacy values are flows saved before item A: they keep their files until edited
				"rateControl": typedRef(described(withDefault(enum([]any{"quality", "level", "vbr", "quality_legacy", "vbr_legacy"}, []string{"Quality", "Bitrate level", "VBR with a max", "Quality with a level (earlier)", "VBR (earlier)"}), "quality", "Rate control", "segmented"),
					"Bitrate level aims at the level's bitrate for the new file's height. VBR with a max keeps the quality steady and never goes above your limit. Quality has no bitrate cap.")),
				"vbrMaxKbps": typedRef(described(map[string]any{"type": "integer", "minimum": 100, "maximum": 500000, "title": "Max bitrate (kbps)"},
					"The most data each second may use with VBR. A file never gets more than it has now.")),
			}, []string{"quality", "format", "resolution", "maxBitrate", "speed"})}),
		def(StepDefinition{Type: TypeAudio, RunsAs: RunsPlan, Category: "audio", Name: "Audio", Icon: "audio", TypeLabel: "Audio",
			Description: "Which sound tracks the new file has.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"audio": typedRef(withDefault(audio, "stereo_aac", "Audio", "")),
				"onlyMyLanguages": typedRef(map[string]any{"type": "boolean", "default": true, "title": "Only my languages",
					// design's words (02:38Z), true to planAudio: with none matching, a stereo choice keeps the main track only
					"description": "Keeps only tracks in your languages (Settings > General). Tracks with no language set are always kept. If none match, a stereo choice keeps the main track; the other choices keep all tracks."}),
			}, []string{"audio", "onlyMyLanguages"})}),
		def(StepDefinition{Type: TypeSubtitles, RunsAs: RunsPlan, Category: "subtitles", Name: "Subtitles", Icon: "subtitles", TypeLabel: "Subtitles",
			Description: "Which subtitles the new file keeps.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment,
			SideEffects: "none", ConfigSchema: object(map[string]any{
				"subtitles": typedRef(withDefault(subtitles, "keep_all", "Subtitles", "")),
			}, []string{"subtitles"})}),
		def(StepDefinition{Type: TypeContainer, RunsAs: RunsPlan, Category: "container", Name: "File type", Icon: "file", TypeLabel: "Container",
			Description: "The new file's type.", Inputs: []Port{in}, Outputs: []Port{success}, RunsWith: encodeSegment, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"fileType": typedRef(withDefault(hinted(enum([]any{"mkv", "mp4", "webm", "keep"}, []string{"MKV", "MP4", "WebM", "Keep as is"}), []string{
					"Holds every kind of track; the usual choice for Plex and Jellyfin.",
					"Best for phones, browsers and Apple TV. Files with picture subtitles are left alone (the reason is in Jobs) unless the Subtitles step leaves those out. Styled subtitles become plain text. Surround formats MP4 can't hold become E-AC-3.",
					"For web browsers. Only with AV1 or VP9 video and Opus audio.",
					"Each file keeps its current type. A file whose type can't hold the chosen video or audio becomes MKV."}), "mkv", "File type", "segmented")),
			}, []string{"fileType"})}),
		def(StepDefinition{Type: TypeVerify, RunsAs: RunsVerify, Category: "verify", Name: "Check the result", Icon: "check", TypeLabel: "Check",
			Description: "The new file must play through, look like the original and hold what you chose. If not, the original stays.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "none"}),
		def(StepDefinition{Type: TypeCommit, RunsAs: RunsCommit, Category: "output", Name: "Save the result", Icon: "save", TypeLabel: "Output",
			Description: "Where the new file goes, and what happens to the original.", Inputs: []Port{in},
			Outputs: []Port{success, errPort}, AlwaysOn: true, SideEffects: "commits",
			ConfigSchema: object(map[string]any{
				"policy": typedRef(withDefault(hinted(enum([]any{"separate_folder", "replace", "beside"},
					[]string{"Save to a separate folder", "Replace the original", "Put the new file next to the original"}), []string{
					"New files go to the folder below, in the same sub-folders. Your originals aren't changed, so no space is freed until you remove them yourself.",
					"The original is kept for the days set in Settings > Safety, then deleted. This frees space. You can get an original back from its job.",
					"Both stay. Nothing is deleted."}), "separate_folder", "Save the result", "")),
				"folderMode": typedRef(described(withDefault(hinted(enum([]any{"next_to_library", "one_folder"}, []string{"Next to each library", "One folder for all"}), []string{
					"Each library gets a folder beside it, e.g. /media/movies (shrunk). Same disk, and it works when one workflow serves several libraries.",
					"Every converted file goes into the folder below, in the same sub-folders as in its library."}), "next_to_library", "Where new files go", ""),
					"Pick a folder next to each library, or one folder for all of them. Your originals stay where they are.")),
				"folder": templated(map[string]any{"type": "string", "title": "Folder", "description": "New files go to this folder, in the same sub-folders. You can use variables, like {{library}}.",
					"x-placeholder": "/media/movies (shrunk)", "x-ui": map[string]any{"widget": "folder"}}),
				// W4b: names from {{name}} variables. Empty keeps today's names, so nothing changes for a
				// workflow saved before these fields existed.
				"outputName": templated(described(map[string]any{"type": "string", "title": "Output file name", "maxLength": 200},
					"Leave empty to keep the original's name. You can use variables, like {{title}} (smaller).")),
				"srtName": templated(described(map[string]any{"type": "string", "title": "Subtitle file name", "maxLength": 200},
					"Leave empty to keep today's names. You can use variables, like {{title}}.{{sub_lang}}{{sub_kind}}.srt.")),
				"trackTitle": templated(described(map[string]any{"type": "string", "title": "Audio track title", "maxLength": 200},
					"Leave empty to keep each track's own title. You can use variables, like {{title}} ({{resolution}}).")),
				// R8: checked after "Check the result" passes, before anything is moved. A new file bigger than its original is
				// not saved when "keep"; "replace" saves it anyway. An absent value reads as replace (old workflows).
				"ifBigger": typedRef(withDefault(hinted(enum([]any{"keep", "replace"}, []string{"Keep the original", "Replace anyway"}), []string{
					"The new file is thrown away and the original stays exactly as it is. Applies to every choice above.",
					"The new file is used even though it is bigger. If it replaces the original, the original is kept for the days set in Settings > Safety, then deleted."}),
					"keep", "If the new file is bigger than the original", "")),
			}, []string{"policy"})}),
		// the earlier install's step converts, checks and saves as one run, so its Next carries the file saved
		def(StepDefinition{Type: TypeLegacy, RunsAs: RunsPlan, Category: "video", Name: "Earlier install's settings", Icon: "history", TypeLabel: "Video",
			FileOn:      map[string]string{"success": FileSaved, "skipped": FileOriginal, "error": FileOriginal},
			Description: "The settings this workflow had before workflows existed. They run exactly as before; to change them, start from the Shrink videos steps.",
			Inputs:      []Port{in}, Outputs: []Port{success, skipped, errPort}, AlwaysOn: true, SideEffects: "commits", Deprecated: true, ReadOnly: true,
			ConfigSchema: object(nil, nil)}), // its config (the template) is kept as it is: no form
		// V6a: branches. Each output port has its own words (ui-shop 02:05Z): the UI never makes them up.
		def(StepDefinition{Type: TypeCheck, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Check the file", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by what it is. Every condition you set must hold for Yes.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"codec": described(map[string]any{"type": "array", "title": "Video format is one of", "items": enum([]any{"hevc", "h264", "av1", "vp9", "mpeg4", "mpeg2video", "vc1", "wmv3"},
					[]string{"HEVC", "H.264", "AV1", "VP9", "MPEG-4", "MPEG-2", "VC-1", "WMV"})}, "Leave empty for any."),
				"minBitrateKbps": typedRef(described(map[string]any{"type": "integer", "minimum": 0, "title": "Bitrate is at least (kbps)"}, "0 for any.")),
				"resolution":     typedRef(withDefault(enum([]any{0, 720, 1080, 2160}, []string{"Any", "At least 720p", "At least 1080p", "4K (2160p)"}), 0, "Resolution", "")),
				"hdr":            typedRef(withDefault(enum([]any{"", "yes", "no"}, []string{"Either", "HDR", "Not HDR"}), "", "HDR", "segmented")),
				"dolbyVision":    typedRef(withDefault(enum([]any{"", "yes", "no"}, []string{"Either", "Dolby Vision", "Not Dolby Vision"}), "", "Dolby Vision", "segmented")),
				"fps":            typedRef(described(map[string]any{"type": "number", "minimum": 0, "maximum": 1000, "title": "Frame rate is at least (fps)"}, "0 for any.")),
				"size":           typedRef(described(map[string]any{"type": "integer", "minimum": 0, "title": "File is at least (MB)"}, "0 for any.")),
				"container": described(map[string]any{"type": "array", "title": "File type is one of", "items": enum([]any{"mkv", "mp4", "avi", "mov", "webm", "ts", "m2ts", "wmv", "flv", "mpg"},
					[]string{"MKV", "MP4", "AVI", "MOV", "WebM", "TS", "M2TS", "WMV", "FLV", "MPG"})}, "Leave empty for any."),
				// FB1b: seven more facts
				"bitDepth":         withDefault(enum([]any{0, 8, 10, 12}, []string{"Any", "8-bit", "10-bit", "12-bit"}), 0, "Bit depth", "segmented"),
				"audioCodec":       described(map[string]any{"type": "array", "title": "Some audio track is one of", "items": enum([]any{"aac", "ac3", "eac3", "dts", "truehd", "flac", "opus", "mp3", "pcm_s16le", "vorbis"}, []string{"AAC", "AC-3", "E-AC-3", "DTS", "TrueHD", "FLAC", "Opus", "MP3", "PCM", "Vorbis"})}, "Leave empty for any."),
				"minChannels":      described(withDefault(enum([]any{0, 2, 6, 8}, []string{"Any", "Stereo or more", "5.1 or more", "7.1"}), 0, "Audio channels", ""), "Some audio track has at least this many."),
				"audioLanguage":    described(map[string]any{"type": "array", "title": "Some audio track is in", "items": map[string]any{"type": "string", "maxLength": 3}}, "Language codes such as eng, deu, jpn. Leave empty for any."),
				"subtitleLanguage": described(map[string]any{"type": "array", "title": "Some subtitle track is in", "items": map[string]any{"type": "string", "maxLength": 3}}, "Language codes such as eng, deu, jpn. Leave empty for any."),
				"minMinutes":       described(map[string]any{"type": "integer", "minimum": 0, "maximum": 100000, "title": "Running time is at least (minutes)"}, "0 for any."),
				"chapters":         withDefault(enum([]any{"", "yes", "no"}, []string{"Either", "Has chapters", "No chapters"}), "", "Chapters", "segmented"),
			}, nil)}),
		// FB1b questions: each sends the file one of two ways; every condition set must hold for Yes.
		def(StepDefinition{Type: TypeCheckName, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Check the file name", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by its name or its path.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"where":         withDefault(hinted(enum([]any{"name", "path"}, []string{"The file name", "The whole path"}), []string{"The name with its extension, without its folders.", "The folders and the name."}), "name", "Look at", "segmented"),
				"match":         withDefault(hinted(enum([]any{"contains", "starts", "ends", "glob", "regex"}, []string{"Contains", "Starts with", "Ends with", "Matches a pattern", "Matches a regular expression"}), []string{"The text appears anywhere.", "The text is at the start.", "The text is at the end.", "A pattern with * and ?, like *.1080p.*", "A regular expression, for people who know them."}), "contains", "How", ""),
				"pattern":       map[string]any{"type": "string", "title": "Text or pattern", "maxLength": 300, "x-placeholder": "Remux"},
				"caseSensitive": map[string]any{"type": "boolean", "default": false, "title": "Match upper and lower case exactly"},
			}, []string{"pattern"})}),
		def(StepDefinition{Type: TypeCheckAge, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Check the file's age", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by when it was last changed.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"olderDays":   described(map[string]any{"type": "integer", "minimum": 0, "maximum": 36500, "default": 0, "title": "Last changed at least this many days ago"}, "0 for any."),
				"youngerDays": described(map[string]any{"type": "integer", "minimum": 0, "maximum": 36500, "default": 0, "title": "Last changed at most this many days ago"}, "0 for any."),
			}, nil)}),
		def(StepDefinition{Type: TypeCheckVariable, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Check a variable", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by a variable's value: one the server sets, one of this workflow's, or one a step before set.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"variable": map[string]any{"type": "string", "title": "Variable", "maxLength": 40, "x-placeholder": "resolution", "x-variableName": true},
				"op":       withDefault(enum([]any{"set", "unset", "equals", "contains", "more", "less"}, []string{"Has a value", "Has no value", "Equals", "Contains", "Is more than", "Is less than"}), "equals", "Test", ""),
				"value":    templated(described(map[string]any{"type": "string", "title": "Value", "maxLength": 300}, "A value, or a variable like {{resolution}}.")),
			}, []string{"variable"})}),
		def(StepDefinition{Type: TypeCheckLibrary, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "From this library", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by the library it came from.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"libraries": map[string]any{"type": "array", "title": "Libraries", "items": map[string]any{"type": "string"}, "x-ui": map[string]any{"widget": "libraries"}},
			}, []string{"libraries"})}),
		def(StepDefinition{Type: TypeCheckTag, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Has a tag", Icon: "branch", TypeLabel: "Check",
			Description: "Sends each file one of two ways, by the tags on its record (a 'Tag the file' step sets them; Jobs shows them).",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"tags":  described(map[string]any{"type": "string", "title": "Tags", "maxLength": 1000, "x-placeholder": "remux, needs-review"}, "One or more, separated by commas. Case doesn't matter."),
				"tagOp": withDefault(hinted(enum([]any{"any", "all", "none"}, []string{"Any of them", "All of them", "None of them"}), []string{"Yes when the file has at least one.", "Yes only when it has every one.", "Yes when it has none of them."}), "any", "Yes when the file has", "segmented"),
			}, []string{"tags"})}),
		def(StepDefinition{Type: TypeCheckRandom, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "A share of files", Icon: "branch", TypeLabel: "Check",
			Description: "Sends a chosen share of files down Yes, for trying a setting on some of them. The same file gets the same answer every time.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"share": withDefault(described(map[string]any{"type": "integer", "title": "Share of files (%)", "minimum": 1, "maximum": 99}, "Out of every 100 files, about this many answer Yes."), 10, "Share of files (%)", ""),
			}, []string{"share"})}),
		def(StepDefinition{Type: TypeCheckResult, RunsAs: RunsQuestion, Phase: []string{PhaseCheck, PhaseAfter}, AlwaysOn: true, Category: "filter", Name: "Compare the result", Icon: "branch", TypeLabel: "Check",
			Description: "Sends the file one of two ways by how the new file compares with the original. Between 'Check the result' and 'Save the result', a No can keep the original ('Leave the file alone': the new file is thrown away). After the save, it chooses a tag, a note or a message about the outcome. Whether a bigger file is kept at all is the Save step's choice.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"test": withDefault(hinted(enum([]any{"saved_at_least", "saved_less_than", "bigger", "runtime_within"}, []string{"At least this much was saved", "Less than this much was saved", "The new file is bigger", "The running time is within so many seconds of the original's"}),
					[]string{"Yes when the new file is smaller by at least the percent below.", "Yes when the new file saved less than the percent below (a bigger file counts too).", "Yes when the new file is larger than the original; the percent isn't used.", "Yes when the new file's running time is no more than the seconds below away from the original's, either way. 'Check the result' already refuses a file more than 2% apart; this is for a tighter bound."}), "saved_at_least", "Compare", ""),
				"percent": onlyWhen(withDefault(described(map[string]any{"type": "integer", "title": "Percent of the original's size", "minimum": 1, "maximum": 100}, "What counts as saved: the original's size minus the new file's, as a share of the original."), 10, "Percent of the original's size", ""), "test", []any{"saved_at_least", "saved_less_than"}),
				"seconds": onlyWhen(withDefault(described(map[string]any{"type": "number", "title": "Seconds", "minimum": 0.1, "maximum": 3600}, "How far from the original's running time the new file's may be, in seconds."), 2, "Seconds", ""), "test", []any{"runtime_within"}),
			}, []string{"test"})}),
		def(StepDefinition{Type: TypeCheckHardLinks, RunsAs: RunsQuestion, AlwaysOn: true, Category: "filter", Name: "Shares its data with another file", Icon: "branch", TypeLabel: "Check",
			Description: "Yes when the original has another name on the same disk (a hard link), so converting it would free no space.",
			Inputs:      []Port{in}, Outputs: []Port{yesPort, noPort}, SideEffects: "none"}),
		// FB1b actions: one thing each, before the conversion; the file is not touched.
		def(StepDefinition{Type: TypeSetVariable, RunsAs: RunsAction, Category: "flow", Name: "Set a variable", Icon: "variable", TypeLabel: "Action",
			Description: "Sets a {{name}} the steps after it can read: a value, or a template of other variables.",
			Inputs:      []Port{in}, Outputs: []Port{success}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"name":  map[string]any{"type": "string", "title": "Name", "maxLength": 40, "x-placeholder": "profile", "x-variableName": true},
				"value": templated(described(map[string]any{"type": "string", "title": "Value", "maxLength": 1000, "x-placeholder": "{{title}} ({{resolution}})"}, "A value, or a template of other variables, like {{title}} ({{resolution}}).")),
			}, []string{"name"})}),
		def(StepDefinition{Type: TypeTag, RunsAs: RunsAction, Category: "flow", Name: "Tag the file", Icon: "tag", TypeLabel: "Action",
			Description: "Adds or removes tags on the file's record. Jobs shows them, and a 'Has a tag' step can ask about them on a later run. Variables are filled in. Sets {{tags}} to what the record carries after this step. A tag is cut at 40 characters; a record carries at most 20 tags, so adds past that are left off (the log says so).",
			Inputs:      []Port{in}, Outputs: []Port{success}, SideEffects: "none",
			Publishes: []Published{{Name: "tags", Type: "string", Words: "The file's tags after this step, separated by commas"}},
			ConfigSchema: object(map[string]any{
				"add":    templated(described(map[string]any{"type": "string", "title": "Add", "maxLength": 1000, "x-placeholder": "converted, {{profile}}"}, "One or more, separated by commas. Variables are filled in, like {{profile}}.")),
				"remove": templated(described(map[string]any{"type": "string", "title": "Remove", "maxLength": 1000, "x-placeholder": "needs-review"}, "Tags to take off, separated by commas, like needs-review.")),
			}, nil)}),
		def(StepDefinition{Type: TypeWait, RunsAs: RunsAction, Phase: []string{PhaseBefore}, Category: "flow", Name: "Wait", Icon: "clock", TypeLabel: "Action",
			Description: "Pauses before the conversion: for a number of seconds (the worker waits), or until a time of day (the file goes back to the queue until then, so the worker is free). A file that comes back at that time goes through once; a try that 'Convert again later' sends back waits for the next day's time; a person's Retry now goes through at once when this step queued the file, while a file that 'Convert again later' sent back still waits.",
			Inputs:      []Port{in}, Outputs: []Port{success}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"seconds": described(map[string]any{"type": "integer", "title": "Wait for (seconds)", "minimum": 1, "maximum": 3600}, "Up to an hour. Leave empty when waiting until a time of day."),
				"until":   described(map[string]any{"type": "string", "title": "Or until", "maxLength": 5, "x-placeholder": "02:00"}, "A time of day, like 02:00 (this computer's clock). The file waits in the queue until then."),
			}, nil)}),
		def(StepDefinition{Type: TypeNote, RunsAs: RunsAction, Category: "flow", Name: "Add a note to the log", Icon: "note", TypeLabel: "Action",
			Description: "Writes a line into the job log at this point. Variables are filled in.",
			Inputs:      []Port{in}, Outputs: []Port{success}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"text": templated(described(map[string]any{"type": "string", "title": "Note", "maxLength": 1000}, "Variables are filled in, like {{title}} or {{resolution}}.")),
			}, []string{"text"})}),
		// FB2a actions after the save: each on the new file in place; one that doesn't work ends the job "done, with a
		// warning", never undoing the save.
		def(StepDefinition{Type: TypeMediaServer, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Tell the media server", Icon: "tell", TypeLabel: "After",
			Description: "Asks the media servers connected in Settings > Connections to look at the folder, so the new file shows up at once.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"which": withDefault(hinted(enum([]any{"all", "plex", "jellyfin"}, []string{"Every connected media server", "Plex only", "Jellyfin only"}), []string{"Each one that is connected and on.", "Only the Plex connections.", "Only the Jellyfin connections."}), "all", "Which", ""),
			}, nil)}),
		def(StepDefinition{Type: TypeDownloader, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Tell the downloader", Icon: "tell", TypeLabel: "After",
			Description: "Asks the downloaders connected in Settings > Connections to re-read the file, so they know its new size and format.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"which": withDefault(hinted(enum([]any{"all", "sonarr", "radarr"}, []string{"Every connected downloader", "Sonarr only", "Radarr only"}), []string{"Each one that is connected and on.", "Only the Sonarr connections.", "Only the Radarr connections."}), "all", "Which", ""),
			}, nil)}),
		def(StepDefinition{Type: TypeNotify, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Send a notification", Icon: "bell", TypeLabel: "After",
			Description: "Sends a message to one of the targets in Settings > Notifications, or to every one that is on. Variables are filled in.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"target": map[string]any{"type": "string", "title": "Send to", "default": "", "maxLength": 200, "description": "A target's name, as in Settings > Notifications. Leave empty for every target that is on."},
				"kind":   withDefault(enum([]any{"info", "good", "warning", "bad"}, []string{"Information", "Good news", "Warning", "Bad news"}), "good", "Kind", "segmented"),
				"text":   templated(described(map[string]any{"type": "string", "title": "Message", "maxLength": 1000, "x-placeholder": "{{title}} is done: {{saved_percent}}% smaller"}, "Variables are filled in, like {{title}} or {{saved_percent}}.")),
			}, []string{"text"})}),
		def(StepDefinition{Type: TypeSidecar, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Write a file beside the result", Icon: "note", TypeLabel: "After",
			Description: "Writes a text file next to the new file: a note, a .nfo, anything your other tools read. Variables are filled in.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "writes_staging",
			ConfigSchema: object(map[string]any{
				"name":     templated(described(map[string]any{"type": "string", "title": "File name", "maxLength": 200, "x-placeholder": "{{title}}.txt"}, "Next to the new file. Variables are filled in, like {{title}}.")),
				"text":     templated(described(map[string]any{"type": "string", "title": "Contents", "maxLength": 20000}, "Variables are filled in, like {{title}} or {{new_size_bytes}}.")),
				"ifExists": withDefault(hinted(enum([]any{"keep", "replace"}, []string{"Keep the file that is there", "Replace it"}), []string{"A file with that name is left as it is; the step says so.", "The file is written over."}), "keep", "If the file exists", "segmented"),
			}, []string{"name", "text"})}),
		def(StepDefinition{Type: TypeChecksum, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Checksum the result", Icon: "shield", TypeLabel: "After",
			Description: "Computes a checksum of the new file and sets {{checksum}} for the steps after it.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "none",
			Publishes: []Published{{Name: "checksum", Type: "string", Words: "The new file's checksum, in hex"}, {Name: "checksum_kind", Type: "string", Words: "Which checksum (sha256, sha1 or md5)"}},
			ConfigSchema: object(map[string]any{
				"kind": withDefault(enum([]any{"sha256", "sha1", "md5"}, []string{"SHA-256", "SHA-1", "MD5"}), "sha256", "Checksum", "segmented"),
			}, nil)}),
		def(StepDefinition{Type: TypeCopy, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Copy the result to a folder", Icon: "copy", TypeLabel: "After",
			Description: "Copies the new file to another folder (the same sub-folders as in its library) and sets {{copy_path}}.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "writes_staging",
			Publishes: []Published{{Name: "copy_path", Type: "string", Words: "Where the copy was written"}},
			ConfigSchema: object(map[string]any{
				"folder":   templated(described(map[string]any{"type": "string", "title": "Folder", "x-placeholder": "/backup/converted", "x-ui": map[string]any{"widget": "folder"}}, "The copy goes here, in the same sub-folders. Variables are filled in, like {{library}}.")),
				"name":     templated(described(map[string]any{"type": "string", "title": "File name", "maxLength": 200}, "Leave empty to keep the new file's name. Variables are filled in, like {{title}}.")),
				"ifExists": withDefault(hinted(enum([]any{"keep", "replace"}, []string{"Keep the file that is there", "Replace it"}), []string{"A file with that name is left as it is; the step says so.", "The file is written over."}), "keep", "If the file exists", "segmented"),
			}, []string{"folder"})}),
		def(StepDefinition{Type: TypeCommand, RunsAs: RunsAction, Phase: []string{PhaseAfter}, Category: "flow", Name: "Run a command", Icon: "terminal", TypeLabel: "After",
			Description: "Runs a program of yours with the new file's path and variables as arguments. The program's output goes to the job log and into {{command_output}}; an exit code other than 0 is a warning. Off until \"Workflows may run commands\" is on in Settings > Safety, because the program runs as this app's user.",
			Inputs:      []Port{in}, Outputs: []Port{success, errPort}, SideEffects: "runs_program",
			Publishes: []Published{{Name: "command_output", Type: "string", Words: "What the program printed (the first 4,000 characters)"}},
			ConfigSchema: object(map[string]any{
				"program":    described(map[string]any{"type": "string", "title": "Program", "maxLength": 1000, "x-placeholder": "/usr/local/bin/after-convert"}, "The program's path, or its name when it is on the PATH. It is started directly: no shell, so nothing is expanded."),
				"arguments":  templated(described(map[string]any{"type": "string", "title": "Arguments", "maxLength": 4000, "x-placeholder": "\"{{output_path}}\" {{saved_percent}}"}, "Separated by spaces; put double quotes around one with a space in it. Variables are filled in after the split, so a value with spaces or quotes stays one argument, like {{output_path}} or {{title}}.")),
				"timeoutSec": withDefault(described(map[string]any{"type": "integer", "title": "Give up after (seconds)", "minimum": 1, "maximum": 3600}, "The program is stopped after this long."), 60, "Give up after (seconds)", ""),
			}, []string{"program"})}),
		def(StepDefinition{Type: TypeLookupDownloader, RunsAs: RunsAction, Phase: []string{PhaseBefore}, Category: "flow", Name: "Ask the downloader for details", Icon: "tell", TypeLabel: "Action",
			Description: "Asks the downloaders connected in Settings > Connections what this file is, and sets {{title_clean}}, {{year}}, {{season}}, {{episode}} and {{original_language}} for the steps after it. A file no downloader knows leaves them empty.",
			Inputs:      []Port{in}, Outputs: []Port{success}, SideEffects: "none",
			Publishes: []Published{{Name: "title_clean", Type: "string", Words: "The show's or film's title, as the downloader has it"}, {Name: "year", Type: "string", Words: "Its year"},
				{Name: "season", Type: "string", Words: "The season number (an episode only)"}, {Name: "episode", Type: "string", Words: "The episode number (an episode only)"}, {Name: "original_language", Type: "string", Words: "Its original language, as the downloader names it"}},
			ConfigSchema: object(map[string]any{
				"which": withDefault(hinted(enum([]any{"all", "sonarr", "radarr"}, []string{"Every connected downloader", "Sonarr only", "Radarr only"}), []string{"The first one that knows the file answers.", "Only the Sonarr connections.", "Only the Radarr connections."}), "all", "Which", ""),
			}, nil)}),
		def(StepDefinition{Type: TypeRequeue, RunsAs: RunsEnd, Category: "flow", Name: "Convert again later", Icon: "redo", TypeLabel: "End",
			Description: "The file goes back to the queue and is tried again after a while, at most so many times; then it stops as failed with the reason below. For files another program hasn't finished with.",
			Inputs:      []Port{in}, Outputs: []Port{}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"afterMin": withDefault(described(map[string]any{"type": "integer", "title": "Try again after (minutes)", "minimum": 1, "maximum": 10080}, "Up to a week."), 60, "Try again after (minutes)", ""),
				"maxTries": withDefault(described(map[string]any{"type": "integer", "title": "At most (tries)", "minimum": 1, "maximum": 100}, "Counting every try of this file."), 5, "At most (tries)", ""),
				"reason":   described(map[string]any{"type": "string", "title": "When the tries are used up", "maxLength": 200, "x-placeholder": "The download never finished"}, "The job's failure words after the last try."),
			}, nil)}),
		def(StepDefinition{Type: TypeGoto, RunsAs: RunsEnd, Category: "flow", Name: "Hand to another workflow", Icon: "workflow", TypeLabel: "End",
			Description: "The file goes on to another published workflow, from its start: that workflow's own questions, steps and Save the result apply to it. A workflow can't hand files back to this one, and the other workflow must be published.",
			Inputs:      []Port{in}, Outputs: []Port{}, SideEffects: "none",
			ConfigSchema: object(map[string]any{
				"workflow": described(map[string]any{"type": "string", "title": "Workflow", "maxLength": 20, "x-ui": map[string]any{"widget": "workflow"}}, "A published workflow other than this one."),
			}, []string{"workflow"})}),
		def(StepDefinition{Type: TypeFail, RunsAs: RunsEnd, Category: "flow", Name: "Stop as failed", Icon: "stop", TypeLabel: "End",
			Description: "The run ends as failed with the reason below, and the original is left as it is.",
			Inputs:      []Port{in}, Outputs: []Port{}, SideEffects: "none",
			ConfigSchema: object(map[string]any{"reason": withDefault(map[string]any{"type": "string", "maxLength": 200}, "Stopped by this workflow.", "Reason shown", "")}, nil)}),
		def(StepDefinition{Type: TypeEnd, RunsAs: RunsEnd, Category: "flow", Name: "Leave the file alone", Icon: "stop", TypeLabel: "End",
			Description: "The file is left as it is, and the reason below shows in its history.",
			Inputs:      []Port{in}, Outputs: []Port{}, SideEffects: "none",
			ConfigSchema: object(map[string]any{"reason": withDefault(map[string]any{"type": "string", "maxLength": 200}, "Left alone by this workflow.", "Reason shown", "")}, nil)}),
	}
}

// phaseOf is where a kind of step sits unless its entry says otherwise: an end may follow a question
// (before) or an action after the save; the rest have one phase each.
func phaseOf(runsAs string) []string {
	switch runsAs {
	case RunsInput, RunsQuestion:
		return []string{PhaseBefore}
	case RunsAction:
		return []string{PhaseBefore, PhaseAfter}
	case RunsPlan:
		return []string{PhaseConvert}
	case RunsVerify:
		return []string{PhaseCheck}
	case RunsCommit:
		return []string{PhaseSave}
	case RunsEnd:
		return []string{PhaseBefore}
	}
	return []string{}
}

// fileOnOf is the file's state on each output port unless the entry says otherwise: a plan step's
// Next carries the new file (staged), a save's carries it saved, a question's or a before-action's
// the original; "skipped" and "error" always leave the original as it is.
func fileOnOf(d StepDefinition) map[string]string {
	out := map[string]string{}
	for _, p := range d.Outputs {
		switch {
		case p.Name == "skipped" || p.Name == "error":
			out[p.Name] = FileOriginal
		case d.RunsAs == RunsPlan || d.RunsAs == RunsVerify:
			out[p.Name] = FileStaged
		case d.RunsAs == RunsCommit, d.RunsAs == RunsAction && len(d.Phase) == 1 && d.Phase[0] == PhaseAfter:
			out[p.Name] = FileSaved
		default:
			out[p.Name] = FileOriginal
		}
	}
	return out
}

// hinted adds a hint per choice (shown when it is chosen), beside x-enumLabels.
func hinted(s map[string]any, hints []string) map[string]any {
	s["x-enumHints"] = hints
	return s
}

// onlyWhen marks a setting the form shows only while another setting (key) has one of the values: the server
// ignores it otherwise, so the form has no reason to show it. {"x-onlyWhen": {key: values}}.
func onlyWhen(s map[string]any, key string, values []any) map[string]any {
	s["x-onlyWhen"] = map[string]any{key: values}
	return s
}

func described(s map[string]any, d string) map[string]any {
	s["description"] = d
	return s
}

// templated marks a string field that takes {{name}} variables ("x-variables": true, design-joblog-vars
// section 3): the editor shows the variable picker there, and the publish check reads the same flag, so
// the fields that take variables and the fields that are checked are one list.
func templated(s map[string]any) map[string]any {
	s["x-variables"] = true
	return s
}

// typedRef marks a setting that takes a number or a choice (not text) and may hold one whole variable
// reference, {{name}} (W1, workflow-architecture.md 2.2 rule 3): the value it resolves to is checked
// against the setting's own schema. A reference with text around it is refused, and so is one to a
// secret: a secret only fills a field that takes text.
func typedRef(s map[string]any) map[string]any {
	s["x-variableRef"] = true
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
