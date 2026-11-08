// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library's setting is its flow seen through an editor (UI-API-CONTRACT.md "Libraries", addenda 2, 4,
// 6, 7, 9, 13, 14): the Basic slider (slice 16) and the Standard selectors (slice 25a).

func sliderTable() map[string]any { return map[string]any{"positions": rules.Slider} }

// settingView is GET /api/libraries/{id}/setting. kind is derived (addendum 6): "slider" for a flow equal
// to a slider position, "selectors" for one the Standard editor shows, "custom" for the rest (a legacy
// flow, or one made in the builder). A slider or selectors flow also carries its selector values, so the
// Standard editor can open either. Every answer says which choices this version can save.
func settingView(f jobs.Flow) map[string]any {
	kind, _ := f.Template.Label()
	v := map[string]any{"kind": kind, "positions": rules.Slider,
		"availableFormats": rules.Available(), "availableAudio": rules.AvailableAudio,
		"availableSubtitles": rules.AvailableSubtitles, "availableFileTypes": rules.AvailableFileTypes,
		"warnings": f.Template.Warnings()} // the rough-picture warning is added by the routes (flowWarnings)
	if s, ok := f.Template.SelectorsView(); ok {
		b, _ := json.Marshal(s)
		json.Unmarshal(b, &v)
	}
	ladder := map[string]any{"vp9": rules.Ladder["hevc"]} // S6: Max bitrate's numbers (VP9 uses HEVC's)
	for f, row := range rules.Ladder {
		ladder[f] = row
	}
	v["bitrateLadder"] = ladder
	if f.Template.Kind == rules.KindSlider { // Basic (S4): its audio choice and fixed facts
		v["becomes"] = rules.BasicBecomes
	}
	if f.Template.Kind == rules.KindSelectors && f.Template.Selectors != nil { // "Switch to Basic" (control level per flow)
		v["fitsBasic"] = f.Template.Selectors.FitsBasic()
	}
	return v
}

// selectorFields are the Standard selectors (UI-API-CONTRACT.md addendum 2): any of them in a PUT body
// makes it a selectors save.
var selectorFields = map[string]bool{"speed": true, "format": true, "resolution": true, "maxbitrate": true, "audio": true,
	"subtitles": true, "filetype": true, "afterconverting": true, "ignore": true, "includedolbyvision": true, "outputdir": true, "onlymylanguages": true}

// readSelectors is a selectors body: what it leaves out is the quality's own value. A client's kind,
// positions, available*, the removed order, the removed minSavingPct and skipSmall (always
// standardize) and skipHdr (S3: replaced by includeDolbyVision) are ignored: accepted for one release, so
// the current page keeps working.
// settingEcho are the fields a setting answer carries that a client may send back unchanged: ignored.
var settingEcho = map[string]bool{"kind": true, "positions": true, "order": true, "availableformats": true, "availableaudio": true,
	"availablesubtitles": true, "availablefiletypes": true, "warnings": true, "sharedflowchoice": true, "fitsbasic": true, "becomes": true, "bitrateladder": true, "notes": true, "minsavingpct": true, "skipsmall": true, "skiphdr": true}

// readSelectors reads a selectors body (see below); langs are the person's languages, needed for
// "Keep only my languages".
func readSelectors(body map[string]json.RawMessage, langs []string) (rules.Selectors, error) {
	s, err := readSelectorsBody(body)
	if err != nil {
		return s, err
	}
	if (s.Audio == "as_is" && s.OnlyMyLanguages || s.Subtitles == "my_languages") && len(langs) == 0 { // "my languages, as it is" needs them
		return s, &rules.SelectorError{Code: "languages_needed", Message: rules.LanguagesNeeded}
	}
	return s, nil
}

func readSelectorsBody(body map[string]json.RawMessage) (rules.Selectors, error) {
	var q struct{ Quality int }
	if b, err := json.Marshal(body); err != nil || json.Unmarshal(b, &q) != nil {
		return rules.Selectors{}, &rules.SelectorError{Code: "invalid_json", Message: "The request body is not valid JSON."}
	}
	if _, ok := rules.SliderAt(q.Quality); !ok {
		return rules.Selectors{}, &rules.SelectorError{Code: "invalid_quality", Message: "quality must be a slider position from 1 to 5. Nothing was changed."}
	}
	s := rules.DefaultSelectors(q.Quality)
	in := map[string]json.RawMessage{}
	for k, v := range body {
		switch k = strings.ToLower(k); {
		case selectorFields[k] || k == "quality":
			in[k] = v
		case settingEcho[k]:
		default: // e.g. audioBitrate: Standard has no such choice (slice 25b)
			return s, &rules.SelectorError{Code: "invalid", Message: "There is no choice called " + k + ". Nothing was changed."}
		}
	}
	b, _ := json.Marshal(in)
	if err := json.Unmarshal(b, &s); err != nil {
		return s, &rules.SelectorError{Code: "invalid", Message: "A choice has the wrong kind of value (" + err.Error() + "). Nothing was changed."}
	}
	if s.Ignore == nil {
		s.Ignore = []string{}
	}
	s.MaxBitrate = rules.OldBitrateLevel(s.MaxBitrate)   // a page from before S6 sends Mbps: its nearest level, for one release
	if !slices.Contains(rules.AvailableAudio, s.Audio) { // and 25b's audio values: design E's map, for one release
		s.Audio, s.OnlyMyLanguages, _ = rules.StandardAudio(s.Audio, s.OnlyMyLanguages)
	}
	return s, s.Validate()
}

func settingRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	libID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return 0, false
		}
		return id, true
	}
	reply := func(w http.ResponseWriter, l jobs.Library, err error) {
		var shared jobs.ErrFlowShared
		switch {
		case errors.As(err, &shared):
			writeErrorWith(w, http.StatusConflict, "flow_shared",
				"Other libraries use this setting too ("+strings.Join(shared.UsedBy, ", ")+"). Change it for all of them, or give this library its own copy. Nothing was changed.",
				map[string]any{"usedBy": shared.UsedBy})
			return
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		case err != nil:
			serverError(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow() // its files are looked at again with the new setting
		}
		f, err := store.GetFlow(l.FlowID)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, withWarnings(store, f))
	}

	api.HandleFunc("GET /api/settings/slider", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sliderTable())
	})
	api.HandleFunc("GET /api/libraries/{id}/setting", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		l, err := store.GetLibrary(id)
		if err != nil {
			reply(w, l, err)
			return
		}
		f, err := store.GetFlow(l.FlowID)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, withWarnings(store, f))
	})
	api.HandleFunc("PUT /api/libraries/{id}/setting", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		var body map[string]json.RawMessage
		if !readJSON(w, r, &body) {
			return
		}
		var choice struct{ SharedFlowChoice string }
		if b, err := json.Marshal(body); err == nil {
			json.Unmarshal(b, &choice)
		}
		if c := choice.SharedFlowChoice; c != "" && c != "change_both" && c != "make_copy" {
			apiError(w, http.StatusBadRequest, "invalid_choice", "sharedFlowChoice must be change_both or make_copy. Nothing was changed.")
			return
		}
		var cur rules.Template // a Basic save without audio keeps the flow's own (S4)
		if l, err := store.GetLibrary(id); err == nil {
			if f, err := store.GetFlow(l.FlowID); err == nil {
				cur = f.Template
			}
		}
		t, ok := settingTemplate(w, store, body, cur)
		if !ok {
			return
		}
		if l, err := store.GetLibrary(id); err == nil && !outputUsable(w, store, l.FlowID, t) {
			return
		}
		l, err := store.SaveTemplate(id, t, choice.SharedFlowChoice)
		reply(w, l, err)
	})
	// A flow's own setting (the control-level slice: the flow page edits it): the same bodies and answers
	// as a library's. A PUT changes the flow for every library that uses it, and its kind follows the
	// body (that is how "Switch to Standard/Basic" takes effect). Advanced and earlier flows are read-only.
	flowSetting := func(w http.ResponseWriter, r *http.Request) (jobs.Flow, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err == nil && id > 0 {
			f, err := store.GetFlow(id)
			if err == nil {
				return f, true
			}
			if !errors.Is(err, jobs.ErrNotFound) {
				serverError(w, err)
				return jobs.Flow{}, false
			}
		}
		apiError(w, http.StatusNotFound, "not_found", "There is no such flow.")
		return jobs.Flow{}, false
	}
	// The folder for new files (S6): may it be used? The same check runs when a flow is saved with it.
	api.HandleFunc("POST /api/flows/{id}/check-output", func(w http.ResponseWriter, r *http.Request) {
		f, ok := flowSetting(w, r)
		if !ok {
			return
		}
		var in struct{ Path string }
		if !readJSON(w, r, &in) {
			return
		}
		writeJSON(w, map[string]any{"blockers": outputBlockers(store, f.ID, strings.TrimSpace(in.Path)), "notes": []string{}})
	})
	// Warnings for a setting being edited (S6, contract 21:10Z item 7): no side effects, nothing saved.
	api.HandleFunc("POST /api/flows/{id}/warnings", func(w http.ResponseWriter, r *http.Request) {
		f, ok := flowSetting(w, r)
		if !ok {
			return
		}
		var body map[string]json.RawMessage
		if !readJSON(w, r, &body) {
			return
		}
		t, ok := settingTemplate(w, store, body, f.Template)
		if !ok {
			return
		}
		writeJSON(w, map[string]any{"warnings": flowWarnings(store, f.ID, t)})
	})
	api.HandleFunc("GET /api/flows/{id}/setting", func(w http.ResponseWriter, r *http.Request) {
		if f, ok := flowSetting(w, r); ok {
			writeJSON(w, withWarnings(store, f))
		}
	})
	api.HandleFunc("PUT /api/flows/{id}/setting", func(w http.ResponseWriter, r *http.Request) {
		f, ok := flowSetting(w, r)
		if !ok {
			return
		}
		var body map[string]json.RawMessage
		if !readJSON(w, r, &body) {
			return
		}
		if lv := f.Template.Level(); lv != "basic" && lv != "standard" {
			apiError(w, http.StatusConflict, "flow_read_only", "This flow can't be changed here: it was made by an earlier install or in a way these editors can't show. Nothing was changed.")
			return
		}
		t, ok := settingTemplate(w, store, body, f.Template)
		if !ok || !outputUsable(w, store, f.ID, t) {
			return
		}
		f, err := store.SetFlowTemplate(f.ID, t)
		if err != nil {
			serverError(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow() // its libraries' files are looked at again with the new setting
		}
		writeJSON(w, withWarnings(store, f))
	})
	api.HandleFunc("POST /api/libraries/{id}/setting/replace-custom", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		l, err := store.ReplaceCustom(id)
		reply(w, l, err)
	})
}

// flowSteps describes a flow in plain words for the flow page (UI-API-CONTRACT.md addendum 8): one
// numbered step each, raw numbers only in detail. Flows the slider or the program's flags made can be
// described; others get their steps with the flow engine (slice 30).
func flowSteps(t rules.Template) []map[string]any {
	var steps []map[string]any
	add := func(kind, label, detail string) {
		s := map[string]any{"n": len(steps) + 1, "kind": kind, "label": label}
		if detail != "" {
			s["detail"] = detail
		}
		steps = append(steps, s)
	}
	keep := func() {
		add("audio", "Keep every audio track as it is", "")
		add("subtitles", "Keep every subtitle as it is", "")
		add("filetype", "Keep the file type", "")
	}
	switch t.Kind {
	case rules.KindSlider, rules.KindSelectors:
		s, ok := t.SelectorsView()
		if !ok {
			return []map[string]any{}
		}
		pos, _ := rules.SliderAt(s.Quality)
		add("start", "Look at each new video file", "")
		if len(s.Ignore) > 0 {
			add("check", "Leave out "+strings.Join(s.Ignore, ", "), "")
		}
		add("check", "Skip files already in an efficient format (HEVC, AV1, VP9) or not a video format this converts", "")
		if s.IncludeDolbyVision { // S3: HDR10 is always converted and kept as HDR
			add("check", "Dolby Vision files become HDR10; profile 5 is left as it is (converting it would show wrong colours)", "")
		} else {
			add("check", "Leave Dolby Vision files as they are", "")
		}
		size := "keeping its picture size and colour depth"
		if s.Resolution != "keep" {
			size = "at most " + s.Resolution + " lines high (never made bigger), keeping its shape and colour depth"
		}
		add("video", "Make the video smaller at "+pos.Name+", "+size,
			fmt.Sprintf("HEVC, CRF %d at 1080p (lower = better quality); %d up to 720p, %d at 2160p; preset %s", pos.CRF, pos.CRF-1, pos.CRF+1, rules.SpeedPresets[s.Speed]))
		if s.MaxBitrate != "none" {
			f := s.Format
			if rules.Ladder[f] == nil {
				f = "hevc"
			}
			add("video", "Limit the bitrate: "+s.MaxBitrate, fmt.Sprintf("at most %g Mbps at 1080p (by the new file's size), never more than the file has", rules.Ladder[f][s.MaxBitrate]["1080"]))
		}
		switch s.Audio {
		case "stereo_aac": // Basic's default (S4)
			add("audio", "Make the audio stereo AAC in your languages", "each kept track: 2 channels, 160 kbps; one already stereo AAC is kept as it is; with no track in your languages, the main one")
			add("audio", "Keep only audio in my languages", "tracks with no language are kept; if none is in your languages, all are kept")
		case "aac", "eac3", "opus":
			add("audio", map[string]string{"aac": "Convert to AAC", "eac3": "Convert to E-AC-3", "opus": "Convert to Opus"}[s.Audio],
				"only tracks it makes smaller; tracks already in that format are kept as they are")
		case "lossless_only":
			add("audio", "Convert lossless tracks only", "to E-AC-3; other tracks are kept as they are")
		default:
			add("audio", "Keep every audio track as it is", "")
		}
		switch s.Subtitles {
		case "my_languages":
			add("subtitles", "Keep only subtitles in my languages", "subtitles with no language are kept")
		case "text_only":
			add("subtitles", "Text subtitles only", "subtitle pictures (PGS, VobSub) are removed")
		case "srt":
			add("subtitles", "Save as separate .srt files", strings.TrimSuffix(rules.SubtitlesStayInside, ".")+"; an existing file is never replaced")
		default:
			add("subtitles", "Keep every subtitle as it is", "")
		}
		if s.FileType == "keep" {
			add("filetype", "Keep the file type", "")
		} else {
			add("filetype", "Make it an "+strings.ToUpper(s.FileType)+" file", "") // MKV and MP4 both start with a vowel sound
		}
		if s.AfterConverting == "beside" {
			add("finish", "Write the new file next to the original and keep the original, once it is checked",
				fmt.Sprintf("picture similarity at least %.3f (SSIM)", pos.MinSSIM))
		} else {
			add("finish", "Replace the original once the new file is checked",
				fmt.Sprintf("picture similarity at least %.3f (SSIM)", pos.MinSSIM))
		}
	case rules.KindLegacy:
		p := t.Params
		add("start", "Look at each new video file", "")
		add("check", fmt.Sprintf("Convert only %s video at %d kbps or more", strings.ToUpper(p.FromCodec), p.MinBitrateKbps), "")
		add("check", "Skip HDR and Dolby Vision files", "")
		add("video", "Make the video smaller", fmt.Sprintf("HEVC, CRF %d (lower = better quality), preset %s", p.CRF, p.Preset))
		keep()
		if p.Output == "beside" {
			add("finish", "Write the new file next to the original and keep the original", "")
		} else {
			add("finish", "Replace the original once the new file is checked", "") // always standardize: no size condition
		}
	default:
		return []map[string]any{}
	}
	return steps
}

// settingTemplate reads a setting body into the template it saves (or writes the error and says false):
// quality, and maybe audio, is a Basic save (S4); any selector field a Standard one (addendum 7). cur is
// the flow's template now: a Basic save without audio keeps its audio.
func settingTemplate(w http.ResponseWriter, store *jobs.Store, body map[string]json.RawMessage, cur rules.Template) (rules.Template, bool) {
	for k := range body { // a field no setting has (audioBitrate, a typo) is refused whatever the shape
		if lk := strings.ToLower(k); !selectorFields[lk] && lk != "quality" && !settingEcho[lk] {
			apiError(w, http.StatusBadRequest, "invalid", "There is no choice called "+k+". Nothing was changed.")
			return rules.Template{}, false
		}
	}
	if q, audio, basic := readBasic(body); basic {
		if _, ok := rules.SliderAt(q); !ok {
			apiError(w, http.StatusBadRequest, "invalid_quality", "quality must be a slider position from 1 to 5. Nothing was changed.")
			return rules.Template{}, false
		}
		if audio != "" && audio != "keep_all" && len(store.Languages()) == 0 {
			apiError(w, http.StatusBadRequest, "languages_needed", rules.LanguagesNeeded)
			return rules.Template{}, false
		}
		if audio == "" && cur.Kind == rules.KindSlider { // not sent (the slider alone): the flow keeps its audio
			audio = cur.Params.Audio
		}
		return rules.BasicTemplate(q, audio), true
	}
	for k := range body {
		if selectorFields[strings.ToLower(k)] {
			s, err := readSelectors(body, store.Languages())
			var se *rules.SelectorError
			if errors.As(err, &se) {
				apiError(w, http.StatusBadRequest, se.Code, se.Message)
				return rules.Template{}, false
			}
			return s.Template(), true
		}
	}
	// neither Basic nor Standard: quality is not a number
	apiError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
	return rules.Template{}, false
}

// readBasic reads a Basic (slider) save (S4): a body whose only choice besides quality is audio, with one
// of Basic's values, or quality alone. audio is "" when it is not sent. A Standard body sends its other
// choices too, so it is never read as Basic.
func readBasic(body map[string]json.RawMessage) (quality int, audio string, ok bool) {
	for k, v := range body {
		switch lk := strings.ToLower(k); {
		case lk == "quality":
			if json.Unmarshal(v, &quality) != nil {
				return 0, "", false
			}
		case lk == "audio":
			if json.Unmarshal(v, &audio) != nil || !slices.Contains(rules.AvailableBasicAudio, audio) {
				return 0, "", false
			}
		case selectorFields[lk], !settingEcho[lk] && lk != "sharedflowchoice":
			return 0, "", false // a Standard choice, or no choice at all (refused as such elsewhere)
		}
	}
	return quality, audio, true
}

// outputUsable refuses (400, the blocker's code) a save whose folder for new files can't be used.
func outputUsable(w http.ResponseWriter, store *jobs.Store, flowID int64, t rules.Template) bool {
	if t.Selectors == nil || t.Selectors.AfterConverting != "folder" {
		return true
	}
	if b := outputBlockers(store, flowID, t.Selectors.OutputDir); len(b) > 0 {
		apiError(w, http.StatusBadRequest, b[0]["code"], b[0]["message"]+" Nothing was changed.")
		return false
	}
	return true
}

// flowWarnings are t's warnings for flow flowID: the template's (25a-2's rename) and design E's
// rough-picture warning over the videos of the flow's libraries (S6).
func flowWarnings(store *jobs.Store, flowID int64, t rules.Template) []string {
	w := t.Warnings()
	if s, ok := t.SelectorsView(); ok && t.Kind == rules.KindSelectors && s.MaxBitrate != "none" {
		if shapes, err := store.FlowShapes(flowID); err == nil {
			if r := rules.RoughWarning(s.MaxBitrate, s.Format, shapes); r != "" {
				w = append(w, r)
			}
		}
	}
	return w
}

// withWarnings is settingView with the flow's full warnings.
func withWarnings(store *jobs.Store, f jobs.Flow) map[string]any {
	v := settingView(f)
	v["warnings"] = flowWarnings(store, f.ID, f.Template)
	return v
}
