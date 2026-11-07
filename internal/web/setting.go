// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
		"availableFormats": rules.AvailableFormats, "availableAudio": rules.AvailableAudio,
		"availableSubtitles": rules.AvailableSubtitles, "availableFileTypes": rules.AvailableFileTypes,
		"warnings": f.Template.Warnings()}
	if s, ok := f.Template.SelectorsView(); ok {
		b, _ := json.Marshal(s)
		json.Unmarshal(b, &v)
	}
	return v
}

// selectorFields are the Standard selectors (UI-API-CONTRACT.md addendum 2): any of them in a PUT body
// makes it a selectors save.
var selectorFields = map[string]bool{"speed": true, "format": true, "resolution": true, "maxbitrate": true, "audio": true,
	"subtitles": true, "filetype": true, "afterconverting": true, "ignore": true, "skiphdr": true, "skipsmall": true, "minsavingpct": true}

// readSelectors is a selectors body: what it leaves out is the quality's own value (so minSavingPct
// follows Quality unless given). A client's kind, positions, available* and the removed order are ignored.
func readSelectors(body map[string]json.RawMessage) (rules.Selectors, error) {
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
		if k = strings.ToLower(k); selectorFields[k] || k == "quality" {
			in[k] = v
		}
	}
	b, _ := json.Marshal(in)
	if err := json.Unmarshal(b, &s); err != nil {
		return s, &rules.SelectorError{Code: "invalid", Message: "A choice has the wrong kind of value (" + err.Error() + "). Nothing was changed."}
	}
	if s.Ignore == nil {
		s.Ignore = []string{}
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
		writeJSON(w, settingView(f))
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
		writeJSON(w, settingView(f))
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
		for k := range body {
			if selectorFields[strings.ToLower(k)] { // the body's shape decides (addendum 7): a selectors save
				s, err := readSelectors(body)
				var se *rules.SelectorError
				if errors.As(err, &se) {
					apiError(w, http.StatusBadRequest, se.Code, se.Message)
					return
				}
				l, err := store.SaveTemplate(id, s.Template(), choice.SharedFlowChoice)
				reply(w, l, err)
				return
			}
		}
		var in struct {
			Quality          int
			SharedFlowChoice string
		}
		if b, err := json.Marshal(body); err != nil || json.Unmarshal(b, &in) != nil {
			apiError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
			return
		}
		if _, ok := rules.SliderAt(in.Quality); !ok {
			apiError(w, http.StatusBadRequest, "invalid_quality", "quality must be a slider position from 1 to 5. Nothing was changed.")
			return
		}
		if c := in.SharedFlowChoice; c != "" && c != "change_both" && c != "make_copy" {
			apiError(w, http.StatusBadRequest, "invalid_choice", "sharedFlowChoice must be change_both or make_copy. Nothing was changed.")
			return
		}
		l, err := store.SaveSlider(id, in.Quality, in.SharedFlowChoice)
		reply(w, l, err)
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
		if s.SkipHDR {
			add("check", "Skip HDR and Dolby Vision files", "")
		}
		if s.SkipSmall {
			add("check", "Skip files that are already small for their picture size", fmt.Sprintf("needs more than %.3f bits per pixel per frame", pos.MinBPP))
		}
		size := "keeping its picture size and colour depth"
		if s.Resolution != "keep" {
			size = "at most " + s.Resolution + " lines high (never made bigger), keeping its shape and colour depth"
		}
		add("video", "Make the video smaller at "+pos.Name+", "+size,
			fmt.Sprintf("HEVC, CRF %d at 1080p (lower = better quality); %d up to 720p, %d at 2160p; preset %s", pos.CRF, pos.CRF-1, pos.CRF+1, rules.SpeedPresets[s.Speed]))
		if s.MaxBitrate != "none" {
			n, _ := strconv.Atoi(s.MaxBitrate)
			add("video", "Limit to "+s.MaxBitrate+" Mbps", fmt.Sprintf("maxrate %dk, bufsize %dk", n*1000, 2*n*1000))
		}
		add("audio", "Keep every audio track as it is", "")
		add("subtitles", "Keep every subtitle as it is", "")
		if s.FileType == "keep" {
			add("filetype", "Keep the file type", "")
		} else {
			add("filetype", "Make it a "+strings.ToUpper(s.FileType)+" file", "")
		}
		smaller := "smaller"
		if s.MinSavingPct > 0 {
			smaller = fmt.Sprintf("at least %d%% smaller", s.MinSavingPct)
		}
		if s.AfterConverting == "beside" {
			add("finish", "Write the new file next to the original and keep the original, once it is checked and "+smaller,
				fmt.Sprintf("picture similarity at least %.3f (SSIM)", pos.MinSSIM))
		} else {
			add("finish", "Replace the original once the new file is checked and "+smaller,
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
			add("finish", "Replace the original once the new file is checked and smaller", "")
		}
	default:
		return []map[string]any{}
	}
	return steps
}
