// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// previewWait is how long a Preview request waits for the result before answering "computing" (the
// page then asks again every few seconds).
var previewWait = 2 * time.Second

// The Preview (UI-API-CONTRACT.md "Preview", addenda 3, 5 and 12): works on a library that is not
// started, reads its files and writes nothing to them.
func previewRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	library := func(w http.ResponseWriter, r *http.Request) (jobs.Library, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		l, gerr := store.GetLibrary(id)
		switch {
		case err != nil || id <= 0 || errors.Is(gerr, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return l, false
		case gerr != nil:
			serverError(w, gerr)
			return l, false
		}
		return l, true
	}

	api.HandleFunc("POST /api/libraries/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		l, ok := library(w, r)
		if !ok {
			return
		}
		var in struct {
			Setting    map[string]json.RawMessage `json:"setting"`
			SampleFile *string                    `json:"sampleFile"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		t, ok := previewTemplate(w, store, l, in.Setting)
		if !ok {
			return
		}
		root, err := jobs.RealPath(l.Path)
		if err != nil {
			root = l.Path // the walk then says the folder cannot be opened: "failed"
		}
		var sample *preview.Sample
		if in.SampleFile != nil && *in.SampleFile != "" {
			path, st, ok := sampleFile(w, root, *in.SampleFile)
			if !ok {
				return
			}
			s := eng.Preview.Sample(r.Context(), path, st, t)
			sample = &s
		}
		res := eng.Preview.Library(l.ID, root, t, previewWait)
		writeJSON(w, struct {
			preview.Result
			Sample *preview.Sample `json:"sample,omitempty"`
		}{res, sample})
	})

	api.HandleFunc("GET /api/libraries/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		l, ok := library(w, r)
		if !ok {
			return
		}
		root, err := jobs.RealPath(l.Path)
		if err == nil {
			var files []preview.Candidate
			if files, err = preview.Files(root, r.URL.Query().Get("q"), 50); err == nil {
				writeJSON(w, map[string]any{"files": files})
				return
			}
		}
		apiError(w, http.StatusConflict, "folder_unreadable", "We can't open this library's folder. Check the path, and that shrink-your-shit is allowed to read it.")
	})
}

// previewTemplate is the setting to preview: the one in the body, or the library's saved one when the
// body has none (addendum 3). The slider is the only editor so far; the selectors come with slice 25.
func previewTemplate(w http.ResponseWriter, store *jobs.Store, l jobs.Library, s map[string]json.RawMessage) (rules.Template, bool) {
	if len(s) == 0 {
		f, err := store.GetFlow(l.FlowID)
		if err != nil {
			serverError(w, err)
			return rules.Template{}, false
		}
		return f.Template, true
	}
	var q int
	if raw, ok := s["quality"]; ok && json.Unmarshal(raw, &q) == nil {
		if _, ok := rules.SliderAt(q); ok {
			for k := range s {
				if k != "quality" && k != "kind" && k != "positions" {
					apiError(w, http.StatusBadRequest, "invalid_setting", "Only slider settings can be previewed in this version.")
					return rules.Template{}, false
				}
			}
			return rules.SliderTemplate(q), true
		}
	}
	apiError(w, http.StatusBadRequest, "invalid_setting", "setting.quality must be a slider position from 1 to 5.")
	return rules.Template{}, false
}

// sampleFile resolves the chosen sample and checks it is a video inside the library's folder.
func sampleFile(w http.ResponseWriter, root, p string) (string, os.FileInfo, bool) {
	inside := func(p string) bool {
		rel, err := filepath.Rel(root, p)
		return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	outside := func() (string, os.FileInfo, bool) {
		apiError(w, http.StatusBadRequest, "outside_library", "Pick a video inside this library's folder.")
		return "", nil, false
	}
	if !filepath.IsAbs(p) || !inside(filepath.Clean(p)) { // as written (the files list gives real paths)
		return outside()
	}
	real, err := jobs.RealPath(p)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found", "That file is not there any more.")
		return "", nil, false
	}
	if !inside(real) { // a link inside that leads out of the folder
		return outside()
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() || !scanner.IsVideo(real) {
		apiError(w, http.StatusBadRequest, "not_a_video", "Pick a video file.")
		return "", nil, false
	}
	return real, st, true
}
