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
			s := eng.Preview.Sample(r.Context(), root, path, st, t)
			sample = &s
		}
		res := eng.Preview.Library(l.ID, root, t, previewWait)
		writeJSON(w, struct {
			preview.Result
			Sample   *preview.Sample `json:"sample,omitempty"`
			Warnings []string        `json:"warnings"` // the previewed setting's, as GET .../setting gives the saved one's
		}{res, sample, t.Warnings()})
	})

	// The first 5 videos of a library, A to Z, with codec, resolution and length ("Is this the right
	// folder?", slice A); called again until stillLooking is false.
	api.HandleFunc("GET /api/libraries/{id}/first-files", func(w http.ResponseWriter, r *http.Request) {
		l, ok := library(w, r)
		if !ok {
			return
		}
		root, err := jobs.RealPath(l.Path)
		if err != nil || eng.FirstFiles == nil {
			apiError(w, http.StatusConflict, "folder_unreadable", "We can't open this library's folder. Check the path, and that shrink-your-shit is allowed to read it.")
			return
		}
		var ignored func(string) bool
		key := root + "\x00library"
		if f, err := store.GetFlow(l.FlowID); err == nil {
			ignored = f.Template.Ignored
			if s, ok := f.Template.SelectorsView(); ok {
				key += "\x00" + strings.Join(s.Ignore, "\x00")
			}
		}
		writeJSON(w, eng.FirstFiles.Get(key, root, ignored))
	})
	// The same for a folder that is not a library yet (Add a library, first-run step 3): checked as
	// POST /api/libraries/check checks it (once per walk), refused for a system folder or one it cannot open.
	api.HandleFunc("POST /api/libraries/first-files", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		if !readJSON(w, r, &in) {
			return
		}
		p := strings.TrimSpace(in.Path)
		if p == "" || !filepath.IsAbs(p) || jobs.DevicePath(p) {
			apiError(w, http.StatusBadRequest, "invalid_path", "Choose a folder by its full path.")
			return
		}
		if eng.Doctor == nil || eng.FirstFiles == nil {
			apiError(w, http.StatusServiceUnavailable, "not_available", "The checks are not available.")
			return
		}
		p = filepath.Clean(p)
		root, err := jobs.RealPath(p)
		if err != nil {
			root = p
		}
		if !eng.FirstFiles.Known(root) {
			// only what stops listing it: a system folder, or one that cannot be opened. No videos, read-only,
			// low space and the converter are the folder check's to say; an empty folder lists nothing (200).
			for _, b := range eng.Doctor.Folder(r.Context(), p).Blockers {
				if b.Code == "system_folder" || b.Code == "unreadable" {
					apiError(w, http.StatusBadRequest, b.Code, b.Message)
					return
				}
			}
		}
		writeJSON(w, eng.FirstFiles.Get(root, root, nil))
	})
	api.HandleFunc("GET /api/libraries/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		l, ok := library(w, r)
		if !ok {
			return
		}
		root, err := jobs.RealPath(l.Path)
		if err == nil {
			var files []preview.Candidate
			if files, err = preview.Files(root, r.URL.Query().Get("q")); err == nil {
				// a list: {items, total}, paged with ?limit=&offset= (default 50; UI-API-CONTRACT.md)
				limit, offset := 50, 0
				if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
					limit = min(n, 500)
				}
				if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
					offset = min(n, len(files))
				}
				writeJSON(w, map[string]any{"items": files[offset:min(offset+limit, len(files))], "total": len(files)})
				return
			}
		}
		apiError(w, http.StatusConflict, "folder_unreadable", "We can't open this library's folder. Check the path, and that shrink-your-shit is allowed to read it.")
	})
}

// previewTemplate is the setting to preview: the one in the body (the slider's quality, or the Standard
// selectors, read as a save reads them), or the library's saved one when the body has none (addendum 3).
func previewTemplate(w http.ResponseWriter, store *jobs.Store, l jobs.Library, s map[string]json.RawMessage) (rules.Template, bool) {
	if len(s) == 0 {
		f, err := store.GetFlow(l.FlowID)
		if err != nil {
			serverError(w, err)
			return rules.Template{}, false
		}
		return f.Template, true
	}
	if q, audio, basic := readBasic(s); basic { // a Basic setting (S4)
		if _, ok := rules.SliderAt(q); !ok {
			apiError(w, http.StatusBadRequest, "invalid_setting", "quality must be a slider position from 1 to 5.")
			return rules.Template{}, false
		}
		if audio != "" && audio != "keep_all" && len(store.Languages()) == 0 {
			apiError(w, http.StatusBadRequest, "languages_needed", rules.LanguagesNeeded)
			return rules.Template{}, false
		}
		t := rules.BasicTemplate(q, audio)
		t.Languages = strings.Join(store.Languages(), ",")
		return t, true
	}
	sel, err := readSelectors(s, store.Languages())
	var se *rules.SelectorError
	if errors.As(err, &se) {
		code := se.Code
		if code != "not_available" && code != "languages_needed" {
			code = "invalid_setting"
		}
		apiError(w, http.StatusBadRequest, code, strings.TrimSuffix(se.Message, " Nothing was changed."))
		return rules.Template{}, false
	}
	t := sel.Template()
	t.Languages = strings.Join(store.Languages(), ",") // the person's, as a saved flow gets them
	return t, true
}

// sampleFile resolves the chosen sample and checks it is a video inside the library's folder. The
// folder (root) is stored resolved (/var is /private/var on macOS, RUNNER~1 a long name on Windows),
// so the file is resolved the same way before it is compared: a missing file through its folder.
func sampleFile(w http.ResponseWriter, root, p string) (string, os.FileInfo, bool) {
	outside := func() (string, os.FileInfo, bool) {
		apiError(w, http.StatusBadRequest, "outside_library", "Pick a video inside this library's folder.")
		return "", nil, false
	}
	if !filepath.IsAbs(p) {
		return outside()
	}
	real, err := jobs.RealPath(p)
	missing := err != nil
	if missing {
		dir, derr := jobs.RealPath(filepath.Dir(p))
		if derr != nil {
			dir = filepath.Clean(filepath.Dir(p))
		}
		real = filepath.Join(dir, filepath.Base(p))
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return outside() // also a link inside that leads out of the folder
	}
	if missing {
		apiError(w, http.StatusNotFound, "not_found", "That file is not there any more.")
		return "", nil, false
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() || !scanner.IsVideo(real) {
		apiError(w, http.StatusBadRequest, "not_a_video", "Pick a video file.")
		return "", nil, false
	}
	return real, st, true
}
