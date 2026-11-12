// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// V5b (UI-API-CONTRACT-v2.md, Libraries; slices/v2-engine/v5-plan.md): the libraries in the contract's
// shape. Adding, renaming, choosing the workflow, removing and scanning go through v1's own handlers,
// with all their checks; pausing, priority and the folder for new files are V5b's.
func v2Libraries(api *http.ServeMux, store *jobs.Store, eng Engine) {
	storeErr := func(w http.ResponseWriter, err error) {
		if errors.Is(err, jobs.ErrNotFound) {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		}
		serverError(w, err)
	}
	view := func(l jobs.Library, counts map[int64]jobs.LibraryCounts) (map[string]any, error) {
		c := counts[l.ID]
		_, _, cards, err := store.FileList(jobs.FileFilter{Show: jobs.CardProcessed, LibraryID: l.ID, Limit: 0})
		if err != nil {
			return nil, err
		}
		f, ferr := store.GetFlow(l.FlowID)
		name, policy := "", any(nil)
		var folder, problem any
		if ferr == nil {
			name = f.Name
			switch s := f.Template.Selectors; {
			case s == nil || s.AfterConverting == "replace" || s.AfterConverting == "":
				policy = "replace"
			case s.AfterConverting == "beside":
				policy = "beside"
			default:
				policy = "separate_folder"
				dir, p := libraryFolder(f.Template, l)
				if p != nil {
					problem = p
				} else {
					folder = dir
					for _, lp := range otherLibraries(store, l.ID) {
						if outputInside(dir, lp) {
							problem = map[string]string{"code": "inside_library", "message": "New files for " + l.Name + " would go to " + dir + ", which is inside the library at " + lp + " (or holds it). Pick a folder in the library's Output tab."}
						}
					}
				}
			}
		}
		var scanning map[int64]int
		if eng.Scanning != nil {
			scanning = eng.Scanning()
		}
		found, isScanning := scanning[l.ID]
		scan := map[string]any{"state": "idle", "progress": nil, "lastScanAt": nilIfEmpty(l.LastScan), "nextScanAt": nil, "scheduleLabel": scheduleLabel(eng.ScanEvery)}
		if isScanning {
			scan["state"], scan["progress"] = "scanning", found
		}
		if l.Started && eng.NextScan != nil {
			if t := eng.NextScan(); !t.IsZero() {
				scan["nextScanAt"] = t.UTC().Format(time.RFC3339)
			}
		}
		status := "ok"
		switch {
		case !l.Started:
			status = "disabled"
		case freeBytes(l.Path) == nil: // time-limited: a hung network mount can't stall the list
			status = "path_missing"
		case isScanning:
			status = "scanning"
		}
		return map[string]any{
			"id": strconv.FormatInt(l.ID, 10), "name": l.Name, "paths": []string{l.Path}, "fileType": "video",
			"enabled": l.Started, "workflowId": strconv.FormatInt(l.FlowID, 10), "workflowName": name, "priority": l.Priority,
			"scan": scan,
			"counts": map[string]int{"files": l.LastScanVideos, "waiting": c.Waiting, "running": c.Working,
				"completed": cards[jobs.CardProcessed], "skipped": cards[jobs.CardSkipped], "failed": cards[jobs.CardFailed]},
			"bytes":  map[string]int64{"freed": c.FreedBytes, "waitingToFree": c.WaitingBytes, "couldFree": c.CouldFreeBytes},
			"output": map[string]any{"policy": policy, "folder": folder, "overrideFolder": nilIfEmpty(l.OverrideFolder), "problem": problem},
			// CAN'T YET: per-library scan frequency (one scan pass walks every library, and a new file is
			// decided on its second look a pass later), include/exclude (the workflow's Ignore list is it)
			"discovery": map[string]any{"subfolders": true, "settleSeconds": int(eng.Settle.Seconds()), "followLinks": false,
				"include": []string{}, "exclude": []string{}, "scanEverySeconds": int(eng.ScanEvery.Seconds())},
			"status": status,
		}, nil
	}
	one := func(w http.ResponseWriter, id int64, code int) {
		l, err := store.GetLibrary(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		counts, err := store.Counts()
		if err != nil {
			serverError(w, err)
			return
		}
		v, err := view(l, counts)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, code, v)
	}
	// v1 answers a request to its own handler (with the caller's sign-in); a refusal is passed on as it is.
	v1 := func(w http.ResponseWriter, r *http.Request, method, path string, body any) (*httptest.ResponseRecorder, bool) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(b))).WithContext(r.Context())
		req.Header.Set("Content-Type", "application/json")
		for _, c := range r.Cookies() {
			req.AddCookie(c)
		}
		for _, h := range []string{"Authorization", "X-Requested-With", "Origin"} {
			if v := r.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
			w.WriteHeader(rec.Code)
			io.Copy(w, rec.Body)
			return rec, false
		}
		return rec, true
	}
	libID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err == nil && id > 0 {
			if _, gerr := store.GetLibrary(id); gerr == nil {
				return id, true
			}
		}
		apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
		return 0, false
	}

	api.HandleFunc("GET /api/v2/libraries", func(w http.ResponseWriter, r *http.Request) {
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		counts, err := store.Counts()
		if err != nil {
			serverError(w, err)
			return
		}
		wf, st := r.URL.Query().Get("workflowId"), r.URL.Query().Get("status")
		items := []map[string]any{}
		for _, l := range libs {
			if wf != "" && strconv.FormatInt(l.FlowID, 10) != wf {
				continue
			}
			v, err := view(l, counts)
			if err != nil {
				serverError(w, err)
				return
			}
			if st == "" || v["status"] == st {
				items = append(items, v)
			}
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})
	api.HandleFunc("GET /api/v2/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := libID(w, r); ok {
			one(w, id, http.StatusOK)
		}
	})
	api.HandleFunc("POST /api/v2/libraries", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name       string   `json:"name"`
			Paths      []string `json:"paths"`
			WorkflowID string   `json:"workflowId"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if len(in.Paths) != 1 {
			apiError(w, http.StatusBadRequest, "invalid", "A library has one folder for now (paths: [one full path]). Nothing was changed.")
			return
		}
		body := map[string]any{"name": in.Name, "path": in.Paths[0]}
		if in.WorkflowID != "" {
			body["flowId"] = in.WorkflowID
		}
		rec, ok := v1(w, r, "POST", "/api/libraries", body)
		if !ok {
			return
		}
		var made struct{ ID json.RawMessage }
		json.Unmarshal(rec.Body.Bytes(), &made)
		id, _ := strconv.ParseInt(strings.Trim(string(made.ID), `"`), 10, 64)
		one(w, id, http.StatusCreated)
	})
	api.HandleFunc("PATCH /api/v2/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		var in map[string]json.RawMessage
		if !readJSON(w, r, &in) {
			return
		}
		bad := func(msg string) { apiError(w, http.StatusBadRequest, "invalid_setting", msg+" Nothing was changed.") }
		known := map[string]bool{"name": true, "enabled": true, "workflowId": true, "priority": true, "overrideFolder": true, "discovery": true}
		for k := range in {
			if !known[k] {
				bad(fmt.Sprintf("%q can't be changed here.", k))
				return
			}
		}
		// everything checked first, then changed
		var enabled *bool
		if raw, ok := in["enabled"]; ok {
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				bad("enabled is true or false.")
				return
			}
			enabled = &b
		}
		var priority *int
		if raw, ok := in["priority"]; ok {
			var p int
			if json.Unmarshal(raw, &p) != nil || p < -1000 || p > 1000 {
				bad("priority is a whole number from -1000 to 1000.")
				return
			}
			priority = &p
		}
		var override *string
		if raw, ok := in["overrideFolder"]; ok {
			dir := ""
			if string(raw) != "null" {
				if json.Unmarshal(raw, &dir) != nil {
					bad("overrideFolder is a folder's full path, or null.")
					return
				}
				dir = strings.TrimSpace(dir)
			}
			if dir != "" {
				if !filepath.IsAbs(dir) {
					bad("Enter the folder's full path.")
					return
				}
				dir = filepath.Clean(dir)
				if b := outputBlockers(store, 0, dir); len(b) > 0 && b[0]["code"] != "low_space" { // low space is a note, not a refusal
					apiError(w, http.StatusBadRequest, b[0]["code"], b[0]["message"]+" Nothing was changed.")
					return
				}
			}
			override = &dir
		}
		if _, ok := in["discovery"]; ok {
			apiError(w, http.StatusBadRequest, "not_available", "A library's scan settings can't be changed yet: every library is scanned on the same schedule. Nothing was changed.")
			return
		}
		if raw, ok := in["workflowId"]; ok {
			if _, ok := v1(w, r, "PUT", "/api/libraries/"+strconv.FormatInt(id, 10)+"/flow", map[string]json.RawMessage{"flowId": raw}); !ok {
				return
			}
		}
		if raw, ok := in["name"]; ok {
			if _, ok := v1(w, r, "PATCH", "/api/libraries/"+strconv.FormatInt(id, 10), map[string]json.RawMessage{"name": raw}); !ok {
				return
			}
		}
		var err error
		if priority != nil {
			err = store.SetLibraryPriority(id, *priority)
		}
		if err == nil && override != nil {
			err = store.SetOverrideFolder(id, *override)
		}
		if err == nil && enabled != nil {
			if *enabled {
				err = store.StartLibrary(id) // resume (the old pages' Start does the same)
			} else {
				err = store.StopLibrary(id) // pause: its waiting jobs stay, not taken; a running one finishes
			}
		}
		if err != nil {
			if errors.Is(err, jobs.ErrSystemFolder) {
				apiError(w, http.StatusBadRequest, "system_folder", "This library is a system folder, so it can't be started. Nothing was changed.")
				return
			}
			storeErr(w, err)
			return
		}
		one(w, id, http.StatusOK)
	})
	api.HandleFunc("DELETE /api/v2/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		if rec, ok := v1(w, r, "DELETE", "/api/libraries/"+strconv.FormatInt(id, 10), nil); ok {
			w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
			w.WriteHeader(rec.Code)
			io.Copy(w, rec.Body)
		}
	})
	api.HandleFunc("POST /api/v2/libraries/{id}/scan", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		if _, ok := v1(w, r, "POST", "/api/libraries/"+strconv.FormatInt(id, 10)+"/scan", map[string]any{}); ok {
			one(w, id, http.StatusOK)
		}
	})
}

// scheduleLabel says how often libraries are looked at, in words.
func scheduleLabel(every time.Duration) string {
	switch {
	case every <= 0:
		return "When you press Scan"
	case every < time.Minute:
		return fmt.Sprintf("Every %d seconds", int(every.Seconds()))
	case every < time.Hour:
		return fmt.Sprintf("Every %d minutes", int(every.Minutes()))
	}
	return fmt.Sprintf("Every %d hours", int(every.Hours()))
}
