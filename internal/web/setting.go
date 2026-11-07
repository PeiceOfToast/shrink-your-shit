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

// A library's setting is its flow seen through an editor (UI-API-CONTRACT.md "Libraries", addenda 4 and
// 6). This slice has the Basic slider; the Standard selectors are slice 25.

func sliderTable() map[string]any { return map[string]any{"positions": rules.Slider} }

// settingView is GET /api/libraries/{id}/setting: the slider position, or "custom" for a flow the
// slider cannot show (a legacy flow, or one made in the builder).
func settingView(f jobs.Flow) map[string]any {
	if f.Template.Kind == rules.KindSlider {
		if _, ok := rules.SliderAt(f.Template.Params.Quality); ok {
			return map[string]any{"kind": "slider", "quality": f.Template.Params.Quality, "positions": rules.Slider}
		}
	}
	return map[string]any{"kind": "custom"}
}

// selectorFields are the Standard selectors (UI-API-CONTRACT.md addendum 2), saved from slice 25 on.
var selectorFields = map[string]bool{"speed": true, "format": true, "resolution": true, "maxbitrate": true, "audio": true,
	"subtitles": true, "filetype": true, "afterconverting": true, "ignore": true, "skiphdr": true, "skipsmall": true, "minsavingpct": true}

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
		for k := range body {
			if selectorFields[strings.ToLower(k)] {
				// Until the selectors are saved (slice 25), dropping them would lose the person's choices.
				apiError(w, http.StatusBadRequest, "not_available", "This version can only save Quality so far; the other choices come in an update. Nothing was changed.")
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
	case rules.KindSlider:
		pos, ok := rules.SliderAt(t.Params.Quality)
		if !ok {
			return []map[string]any{}
		}
		add("start", "Look at each new video file", "")
		add("check", "Skip files already in an efficient format (HEVC, AV1, VP9) or not a video format this converts", "")
		add("check", "Skip files that are already small for their picture size", fmt.Sprintf("needs more than %.3f bits per pixel per frame", pos.MinBPP))
		add("video", "Make the video smaller at "+pos.Name+", keeping its picture size and colour depth",
			fmt.Sprintf("HEVC, CRF %d at 1080p (lower = better quality); %d up to 720p, %d at 2160p; preset %s", pos.CRF, pos.CRF-1, pos.CRF+1, rules.SliderPreset))
		keep()
		add("finish", fmt.Sprintf("Replace the original once the new file is checked and at least %d%% smaller", pos.MinSavingPct),
			fmt.Sprintf("picture similarity at least %.3f (SSIM)", pos.MinSSIM))
	case rules.KindLegacy:
		p := t.Params
		add("start", "Look at each new video file", "")
		add("check", fmt.Sprintf("Convert only %s video at %d kbps or more", strings.ToUpper(p.FromCodec), p.MinBitrateKbps), "")
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
