// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Selectors are the Standard editor's choices (UI-API-CONTRACT.md addenda 2, 9, 13; slice 25a), with the
// API's names. A slider position is the selectors at DefaultSelectors(position): one compiler serves both,
// so a setting left at a position's values behaves exactly like the slider and is saved as it.
type Selectors struct {
	Quality    int    `json:"quality"`
	Speed      string `json:"speed"`      // faster | normal | slower
	Format     string `json:"format"`     // hevc (AvailableFormats)
	Resolution string `json:"resolution"` // keep | 1080 | 720 | 480: the largest picture height; never upscaled
	MaxBitrate string `json:"maxBitrate"` // none | high | medium | low (S6: Ladder): a peak cap beside the quality
	// RateControl (user review 11-13 item A): "level" aims at MaxBitrate's figure for the new file's height
	// (the average; the peak is 1.5x); "vbr" is the quality at CRF 23 with VBRMaxKbps as the peak (no level);
	// "" or "quality" is CRF alone. The *_legacy values are flows saved before item A, compiled as they were.
	RateControl string `json:"rateControl,omitempty"`
	VBRMaxKbps  int    `json:"vbrMaxKbps,omitempty"` // vbr: the person's own limit (kbps)
	// Notices are coded notes shown on the flow until its next publish (migration 38: bitrate_meaning_changed).
	Notices []Notice `json:"notices,omitempty"`
	Audio   string   `json:"audio"` // AvailableAudio (S6: design E's six choices)
	// OnlyMyLanguages keeps only the audio in the person's languages, and tracks with none (S6; on by default).
	OnlyMyLanguages bool   `json:"onlyMyLanguages"`
	Subtitles       string `json:"subtitles"`           // AvailableSubtitles
	FileType        string `json:"fileType"`            // mkv | mp4 | webm | keep (AvailableFileTypes)
	AfterConverting string `json:"afterConverting"`     // replace | beside | folder (S6: with OutputDir)
	OutputDir       string `json:"outputDir,omitempty"` // folder: where the new files go (design E)
	// FolderMode is "next_to_library" (V5a, output-first: each library's own folder beside it, or its
	// override; OutputDir unused) or "" (one folder for all: OutputDir).
	FolderMode string   `json:"folderMode,omitempty"`
	Ignore     []string `json:"ignore"` // folders, files or patterns in the library not looked at
	// IncludeDolbyVision converts Dolby Vision profiles 7 and 8 as HDR10/HLG (S3; replaces skipHdr: HDR10
	// is always converted and kept as HDR). Profile 5 is left as it is whatever this says.
	IncludeDolbyVision bool `json:"includeDolbyVision"`
	// Notes are sentences about this flow shown once (S6: "Audio choices changed in this update…", from
	// migration 19); a save makes a new template without them (contract 23:33Z).
	Notes []string `json:"notes,omitempty"`
}

// What this version can save and run (UI-API-CONTRACT.md addendum 13): the UI shows the rest disabled.
var (
	AvailableFormats   = []string{"hevc"}
	AvailableAudio     = []string{"stereo_aac", "stereo_aac_surround", "eac3_surround", "ac3_surround", "stereo_opus", "as_is"}
	AvailableSubtitles = []string{"keep_all", "my_languages", "text_only", "srt"}
)

// AvailableFileTypes are the file types each "after converting" choice can save (25a beside; 25a-2
// replace, where a new type renames the file: movie.mp4 becomes movie.mkv).
var AvailableFileTypes = map[string][]string{"replace": {"mkv", "mp4", "webm", "keep"}, "beside": {"mkv", "mp4", "webm", "keep"},
	"folder": {"mkv", "mp4", "webm", "keep"}}

// SpeedPresets maps the Speed choice to the x265 preset (normal is the slider's).
var SpeedPresets = map[string]string{"faster": "fast", "normal": SliderPreset, "slower": "slow"}

var (
	resolutions = map[string]int{"keep": 0, "1080": 1080, "720": 720, "480": 480}
	allAudio    = []string{"stereo_aac", "stereo_aac_surround", "eac3_surround", "ac3_surround", "stereo_opus", "as_is"}
	allSubs     = []string{"keep_all", "my_languages", "text_only", "srt"}
	allFormats  = []string{"hevc", "h264", "av1", "vp9"}
)

// DefaultSelectors are slider position q's values as selectors.
func DefaultSelectors(q int) Selectors {
	return Selectors{Quality: q, Speed: "normal", Format: "hevc", Resolution: "keep", MaxBitrate: "none",
		Audio: "stereo_aac", OnlyMyLanguages: true, Subtitles: "keep_all", FileType: "mkv", AfterConverting: "replace", Ignore: []string{}} // design E's defaults (S6)
}

// SelectorError is a choice that cannot be saved: Code is invalid (not a value at all) or not_available
// (a real choice this version cannot do yet).
type SelectorError struct{ Code, Message string }

// Notice is a coded note about a flow (user review 11-13 item A), shown on the flow until its next
// publish. Words through the leads for design.
type Notice struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *SelectorError) Error() string { return e.Message }

// Validate checks every choice. Not-yet choices say so in plain words; nothing is saved then.
func (s Selectors) Validate() error {
	invalid := func(field string, v any) error {
		return &SelectorError{"invalid", fmt.Sprintf("%v is not a choice for %s. Nothing was changed.", v, field)}
	}
	notYet := func(msg string) error { return &SelectorError{"not_available", msg + " Nothing was changed."} }
	if _, ok := SliderAt(s.Quality); !ok {
		return &SelectorError{"invalid_quality", "quality must be a slider position from 1 to 5. Nothing was changed."}
	}
	if _, ok := SpeedPresets[s.Speed]; !ok {
		return invalid("speed", s.Speed)
	}
	switch {
	case !contains(allFormats, s.Format):
		return invalid("format", s.Format)
	case !contains(Available(), s.Format):
		return notYet("This version can only convert to HEVC.")
	}
	if _, ok := resolutions[s.Resolution]; !ok {
		return invalid("resolution", s.Resolution)
	}
	if !contains(BitrateLevels, s.MaxBitrate) {
		return invalid("maxBitrate", s.MaxBitrate)
	}
	switch s.RateControl {
	case "", "quality":
		if s.MaxBitrate != "none" && s.MaxBitrate != "" {
			return &SelectorError{"invalid", "A bitrate level with the quality is now Bitrate level: choose Bitrate level or VBR with a max. Nothing was changed."}
		}
	case "level":
		if s.MaxBitrate == "none" || s.MaxBitrate == "" {
			return &SelectorError{"invalid", "Bitrate level needs Low, Medium or High. Nothing was changed."}
		}
		if s.Format == "av1" { // SVT-AV1 takes a peak limit only with CRF, and the level sets one (found on CT 200)
			return &SelectorError{"invalid", "Bitrate level isn't available for AV1: choose quality, or another format. Nothing was changed."}
		}
	case "vbr": // user review 11-13 item A: the quality at CRF 23 under VBRMaxKbps; no level
		if !VBRFormat(s.Format) { // SVT-AV1 takes a peak limit only with CRF (review a772397: every file failed)
			return &SelectorError{"invalid", "VBR with a max bitrate isn't available for AV1; use Quality only. Nothing was changed."}
		}
		if s.VBRMaxKbps < 100 || s.VBRMaxKbps > 500000 {
			return &SelectorError{"invalid", "Set the max bitrate from 100 to 500,000 kbps. Nothing was changed."}
		}
	case "quality_legacy", "vbr_legacy": // a flow saved before item A (migration 38) runs as it was
	default:
		return invalid("rateControl", s.RateControl)
	}
	switch {
	case !contains(allAudio, s.Audio):
		return invalid("audio", s.Audio)
	}
	switch {
	case !contains(allSubs, s.Subtitles):
		return invalid("subtitles", s.Subtitles)
	case !contains(AvailableSubtitles, s.Subtitles):
		return notYet("This version can only keep every subtitle as it is; the other subtitle choices come in an update.")
	}
	types, ok := AvailableFileTypes[s.AfterConverting]
	if !ok {
		return invalid("afterConverting", s.AfterConverting)
	}
	switch {
	case !contains(AvailableFileTypes["beside"], s.FileType):
		return invalid("fileType", s.FileType)
	case !contains(types, s.FileType):
		return notYet(`Changing the file type needs "Put the new file next to the original" for now; replacing with a new file type comes in an update.`)
	}
	switch {
	case s.AfterConverting == "folder" && s.FolderMode != "" && s.FolderMode != NextToLibrary:
		return invalid("folderMode", s.FolderMode)
	case s.AfterConverting == "folder" && s.FolderMode == "" && (strings.TrimSpace(s.OutputDir) == "" || !filepath.IsAbs(s.OutputDir)):
		return &SelectorError{"output_dir_needed", "Choose the folder for new files (a full path). Nothing was changed."}
	}
	switch { // choices that can't go together (design E, 21:22Z; the page moves the other field first)
	case s.Audio == "stereo_opus" && s.FileType == "mp4":
		return &SelectorError{"invalid", "MP4 files can't hold Opus audio: choose MKV, or another audio choice. Nothing was changed."}
	case s.FileType == "webm" && (s.Format != "av1" && s.Format != "vp9" || s.Audio != "stereo_opus"):
		return &SelectorError{"invalid", "WebM files need AV1 or VP9 video and Stereo Opus audio. Nothing was changed."}
	case s.Format == "vp9" && s.FileType == "mp4":
		return &SelectorError{"invalid", "MP4 files can't hold VP9 video here: choose MKV or WebM. Nothing was changed."}
	}
	for _, p := range s.Ignore {
		if !insideLibrary(p) {
			return &SelectorError{"invalid", fmt.Sprintf("%q is not a folder, file or pattern inside the library. Nothing was changed.", p)}
		}
		if _, err := path.Match(canonIgnore(p), ""); err != nil { // the very pattern Ignored matches
			return &SelectorError{"invalid", fmt.Sprintf("%q is not a pattern this can read. Nothing was changed.", p)}
		}
	}
	return nil
}

// insideLibrary reports whether an Ignore entry is a path inside the library, the same on every OS
// (a library moves between machines; filepath.IsAbs on Windows does not call /etc absolute): not empty,
// no leading / or \, no drive letter (C: or C:\), no UNC path (\\host), and no ".." part, split on both
// separators.
func insideLibrary(p string) bool {
	if strings.TrimSpace(p) == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return false
	}
	if len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z') {
		return false
	}
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

// Template is the flow these selectors make: always a Standard (selectors) flow. Before S4 a Standard
// save at a position's own values became that slider position, the same recipe; since S4 Basic is its
// own recipe (stereo AAC, MKV, HEVC repackaged), so a Standard save never turns into Basic, which would
// drop audio and rename files the person chose to keep (review 6e60704, P1).
func (s Selectors) Template() Template {
	if s.Ignore == nil {
		s.Ignore = []string{}
	}
	c := s
	return Template{Kind: KindSelectors, Selectors: &c, CompilerVersion: CompilerVersion}
}

// SelectorsView is how the Standard editor shows a flow: a slider position as its selectors, a selectors
// flow as saved; false for flows it cannot show (legacy, custom).
func (t Template) SelectorsView() (Selectors, bool) {
	switch t.Kind {
	case KindSlider:
		if _, ok := SliderAt(t.Params.Quality); ok { // Basic: its audio, always MKV (S4)
			s := DefaultSelectors(t.Params.Quality)
			s.Audio, s.FileType = BasicAudio(t.Params.Audio), "mkv"
			return s, true
		}
	case KindSelectors:
		if t.Selectors != nil {
			s := t.Selectors.Upgraded() // shown in S6's terms
			if s.Ignore == nil {
				s.Ignore = []string{}
			}
			return s, true
		}
	}
	return Selectors{}, false
}

// Ignored reports whether rel (a file's path inside its library, any separator) is left out by the
// flow's Ignore list: equal to an entry, inside a folder an entry names, or matching an entry as a
// pattern (on the whole path, or on the file name for a pattern without a slash).
func (t Template) Ignored(rel string) bool {
	if t.Kind != KindSelectors || t.Selectors == nil {
		return false
	}
	rel = slash(rel)
	for _, e := range t.Selectors.Ignore {
		e = canonIgnore(e)
		if rel == e || strings.HasPrefix(rel, e+"/") {
			return true
		}
		if ok, _ := path.Match(e, rel); ok {
			return true
		}
		if !strings.Contains(e, "/") {
			if ok, _ := path.Match(e, path.Base(rel)); ok {
				return true
			}
			for _, dir := range strings.Split(path.Dir(rel), "/") { // a folder name anywhere
				if ok, _ := path.Match(e, dir); ok {
					return true
				}
			}
		}
	}
	return false
}

// slash makes either separator "/", on any OS (an entry typed on Windows works on a Linux server).
func slash(s string) string { return strings.ReplaceAll(filepath.ToSlash(s), `\`, "/") }

// canonIgnore is an Ignore entry as it is matched: "/" separators, cleaned, no trailing "/". Validate
// checks this same form, so an entry that would never match is refused when saved.
func canonIgnore(e string) string { return strings.TrimSuffix(path.Clean(slash(e)), "/") }

// Warnings are what the person should know about a flow's choices before it runs.
func (t Template) Warnings() []string {
	out := []string{}
	s, ok := t.SelectorsView()
	if !ok {
		return out
	}
	if w := RenameWarning(s.AfterConverting, s.FileType); w != "" && t.Kind == KindSelectors { // 25a-2 (Basic's MKV: the update notice, S4)
		out = append(out, w)
	}
	// design (a), 02:30Z: such an MP4 becomes MKV. Only when the original is replaced: beside or in
	// another folder the .mp4 stays, so nothing gets a new name (ui-shop, 02:35Z).
	if t.Kind == KindSelectors && s.AfterConverting == "replace" && s.FileType == "keep" && (s.Format == "vp9" || s.Audio == "stereo_opus") {
		out = append(out, KeepVP9OpusWarning)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Upgraded is s with the values saved before S6 in S6's terms (migration 19 saves the same): a cap in
// Mbps becomes its level; 25b's audio choices become design E's (and "Only my languages").
func (s Selectors) Upgraded() Selectors {
	s.MaxBitrate = OldBitrateLevel(s.MaxBitrate)
	if !contains(AvailableAudio, s.Audio) {
		s.Audio, s.OnlyMyLanguages, _ = StandardAudio(s.Audio, s.OnlyMyLanguages)
	}
	if s.Audio == "stereo_opus" && s.FileType == "mp4" { // 25b's opus in MP4 was valid; S6's Opus needs MKV (review 6cafc6f)
		s.FileType = "mkv"
	}
	return s
}

// KeepVP9OpusWarning is design's warning (02:30Z) for "Keep as is" with VP9 or Opus: an MP4 can't hold
// them, so such a file becomes MKV (the job log says so for each file).
const KeepVP9OpusWarning = "With VP9 or Opus, .mp4 files become .mkv files, so they get a new name. After converting, rescan this library in Radarr, Sonarr, Plex or Jellyfin."

// NextToLibrary is FolderMode's "each library's own folder beside it" (V5a).
const NextToLibrary = "next_to_library"
