// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"strings"
)

// Selectors are the Standard editor's choices (UI-API-CONTRACT.md addenda 2, 9, 13; slice 25a), with the
// API's names. A slider position is the selectors at DefaultSelectors(position): one compiler serves both,
// so a setting left at a position's values behaves exactly like the slider and is saved as it.
type Selectors struct {
	Quality         int      `json:"quality"`
	Speed           string   `json:"speed"`           // faster | normal | slower
	Format          string   `json:"format"`          // hevc (AvailableFormats)
	Resolution      string   `json:"resolution"`      // keep | 1080 | 720 | 480: the largest picture height; never upscaled
	MaxBitrate      string   `json:"maxBitrate"`      // none | 40 | 20 | 10 | 6 | 4 (Mbps): a peak cap beside the quality
	Audio           string   `json:"audio"`           // AvailableAudio
	Subtitles       string   `json:"subtitles"`       // AvailableSubtitles
	FileType        string   `json:"fileType"`        // keep | mkv | mp4 (another than keep only when kept beside, 25a)
	AfterConverting string   `json:"afterConverting"` // replace | beside
	Ignore          []string `json:"ignore"`          // folders, files or patterns in the library not looked at
	SkipHDR         bool     `json:"skipHdr"`         // HDR and Dolby Vision files are left as they are
	SkipSmall       bool     `json:"skipSmall"`       // files already small for their picture size are left as they are
	MinSavingPct    int      `json:"minSavingPct"`    // the new file must be at least this much smaller
}

// What this version can save and run (UI-API-CONTRACT.md addendum 13): the UI shows the rest disabled.
var (
	AvailableFormats   = []string{"hevc"}
	AvailableAudio     = []string{"keep_all"}
	AvailableSubtitles = []string{"keep_all"}
)

// AvailableFileTypes are the file types each "after converting" choice can save (25a: changing the type
// needs the original kept beside; replacing with a new type is 25a-2).
var AvailableFileTypes = map[string][]string{"replace": {"keep"}, "beside": {"keep", "mkv", "mp4"}}

// SpeedPresets maps the Speed choice to the x265 preset (normal is the slider's).
var SpeedPresets = map[string]string{"faster": "fast", "normal": SliderPreset, "slower": "slow"}

// maxBitrates are the cap choices in Mbps ("none": no cap).
var maxBitrates = map[string]int{"none": 0, "40": 40, "20": 20, "10": 10, "6": 6, "4": 4}

var (
	resolutions = map[string]int{"keep": 0, "1080": 1080, "720": 720, "480": 480}
	allAudio    = []string{"keep_all", "my_languages", "aac", "lossless_only"}
	allSubs     = []string{"keep_all", "my_languages", "text_only", "srt"}
	allFormats  = []string{"hevc", "av1"}
)

// DefaultSelectors are slider position q's values as selectors.
func DefaultSelectors(q int) Selectors {
	pos, _ := SliderAt(q)
	return Selectors{Quality: q, Speed: "normal", Format: "hevc", Resolution: "keep", MaxBitrate: "none",
		Audio: "keep_all", Subtitles: "keep_all", FileType: "keep", AfterConverting: "replace", Ignore: []string{},
		SkipHDR: true, SkipSmall: true, MinSavingPct: pos.MinSavingPct}
}

// SelectorError is a choice that cannot be saved: Code is invalid (not a value at all) or not_available
// (a real choice this version cannot do yet).
type SelectorError struct{ Code, Message string }

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
	case !contains(AvailableFormats, s.Format):
		return notYet("This version can only convert to HEVC.")
	}
	if _, ok := resolutions[s.Resolution]; !ok {
		return invalid("resolution", s.Resolution)
	}
	if _, ok := maxBitrates[s.MaxBitrate]; !ok {
		return invalid("maxBitrate", s.MaxBitrate)
	}
	switch {
	case !contains(allAudio, s.Audio):
		return invalid("audio", s.Audio)
	case !contains(AvailableAudio, s.Audio):
		return notYet("This version can only keep every audio track as it is; the other audio choices come in an update.")
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
		return notYet("Changing the file type needs 'Keep both' for now; replacing with a new file type comes in an update.")
	}
	if s.MinSavingPct < 0 || s.MinSavingPct > 90 {
		return invalid("minSavingPct", s.MinSavingPct)
	}
	for _, p := range s.Ignore {
		if strings.TrimSpace(p) == "" || filepath.IsAbs(p) || strings.HasPrefix(path.Clean(filepath.ToSlash(p)), "..") {
			return &SelectorError{"invalid", fmt.Sprintf("%q is not a folder, file or pattern inside the library. Nothing was changed.", p)}
		}
		if _, err := path.Match(filepath.ToSlash(p), ""); err != nil {
			return &SelectorError{"invalid", fmt.Sprintf("%q is not a pattern this can read. Nothing was changed.", p)}
		}
	}
	return nil
}

// Template is the flow these selectors make: the slider position itself when every choice is that
// position's (addendum 6: kind is derived), else a selectors flow.
func (s Selectors) Template() Template {
	if s.Ignore == nil {
		s.Ignore = []string{}
	}
	if s.equal(DefaultSelectors(s.Quality)) {
		return SliderTemplate(s.Quality)
	}
	c := s
	return Template{Kind: KindSelectors, Selectors: &c, CompilerVersion: CompilerVersion}
}

func (s Selectors) equal(o Selectors) bool {
	if len(s.Ignore) != len(o.Ignore) {
		return false
	}
	for i := range s.Ignore {
		if s.Ignore[i] != o.Ignore[i] {
			return false
		}
	}
	s.Ignore, o.Ignore = nil, nil
	return reflect.DeepEqual(s, o)
}

// SelectorsView is how the Standard editor shows a flow: a slider position as its selectors, a selectors
// flow as saved; false for flows it cannot show (legacy, custom).
func (t Template) SelectorsView() (Selectors, bool) {
	switch t.Kind {
	case KindSlider:
		if _, ok := SliderAt(t.Params.Quality); ok {
			return DefaultSelectors(t.Params.Quality), true
		}
	case KindSelectors:
		if t.Selectors != nil {
			s := *t.Selectors
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
	rel = filepath.ToSlash(rel)
	for _, e := range t.Selectors.Ignore {
		e = strings.TrimSuffix(path.Clean(filepath.ToSlash(e)), "/")
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

// Warnings are what the person should know about a flow's choices before it runs.
func (t Template) Warnings() []string {
	if s, ok := t.SelectorsView(); ok && !s.SkipHDR {
		return []string{"HDR and Dolby Vision files will be converted without their HDR information, so they can look washed out. Keep 'Skip HDR' on to leave them as they are."}
	}
	return []string{}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
