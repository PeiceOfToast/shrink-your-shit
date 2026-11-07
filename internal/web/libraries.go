// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Engine is what the API needs from the running scanner and worker. Nil functions do nothing.
type Engine struct {
	ScanNow  func()              // start a scan pass now
	NextScan func() time.Time    // when the next regular pass starts
	StopJob  func(id int64)      // stop a running job (its library was removed)
	PauseNow func()              // stop the running jobs and put them back in the queue (Pause all)
	StopFile func(id int64) bool // stop a running job the person stopped; false if no worker runs it
	Preview  *preview.Previewer  // the dry run (slice 19)

	Doctor  *doctor.Checker // the setup checks (nil: not available)
	DataDir string          // the data folder, for the health report
	Version string
	Logs    *doctor.LogTail // the last log lines, for the health report
}

// libraryItem is a library as GET /api/libraries shows it (UI-API-CONTRACT.md "Libraries").
type libraryItem struct {
	ID           int64   `json:"id,string"` // ids are opaque strings in the API (UI-API-CONTRACT.md)
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	State        string  `json:"state"` // working|idle|needs_attention|not_started (paused: slice 35)
	Started      bool    `json:"started"`
	WorkingFiles int     `json:"workingFiles"`
	WaitingFiles int     `json:"waitingFiles"`
	NextScan     *string `json:"nextScan"`
	FreedBytes   int64   `json:"freedBytes"`   // saved, originals deleted (F1)
	WaitingBytes int64   `json:"waitingBytes"` // saved once the kept originals are deleted (F1)
	FlowID       int64   `json:"flowId,string"`
	SettingLabel string  `json:"settingLabel"`
	SettingKind  string  `json:"settingKind"`
}

// startFrom maps POST /api/libraries "startFrom" to a slider position.
var startFrom = map[string]int{"smallest": 1, "balanced": 3, "better": 4}

// libraryStopWait is how long removing a library waits for its running conversions to end.
var libraryStopWait = 5 * time.Second

func libraryRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	item := func(l jobs.Library, counts map[int64]jobs.LibraryCounts) libraryItem {
		c := counts[l.ID]
		it := libraryItem{ID: l.ID, Name: l.Name, Path: l.Path, Started: l.Started, FlowID: l.FlowID,
			WorkingFiles: c.Working, WaitingFiles: c.Waiting, FreedBytes: c.FreedBytes, WaitingBytes: c.WaitingBytes}
		switch {
		case !l.Started:
			it.State = "not_started"
		case c.Working > 0:
			it.State = "working"
		case c.Failed > 0:
			it.State = "needs_attention"
		default:
			it.State = "idle"
		}
		if l.Started && eng.NextScan != nil {
			if t := eng.NextScan(); !t.IsZero() {
				s := t.UTC().Format(time.RFC3339)
				it.NextScan = &s
			}
		}
		it.SettingKind, it.SettingLabel = rules.KindCustom, "Custom"
		if f, err := store.GetFlow(l.FlowID); err == nil {
			it.SettingKind, it.SettingLabel = f.Template.Label()
		}
		return it
	}
	oneStatus := func(w http.ResponseWriter, status int, l jobs.Library) {
		counts, err := store.Counts()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, status, item(l, counts))
	}
	one := func(w http.ResponseWriter, l jobs.Library) { oneStatus(w, http.StatusOK, l) }
	libID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return 0, false
		}
		return id, true
	}
	storeErr := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
		case errors.Is(err, jobs.ErrPathTaken):
			apiError(w, http.StatusConflict, "path_taken", "Another library already uses this folder.")
		default:
			serverError(w, err)
		}
	}

	api.HandleFunc("GET /api/libraries", func(w http.ResponseWriter, r *http.Request) {
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
		items := []libraryItem{}
		for _, l := range libs {
			items = append(items, item(l, counts))
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})

	api.HandleFunc("POST /api/libraries", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Path, StartFrom string }
		if !readJSON(w, r, &in) {
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			apiError(w, http.StatusBadRequest, "invalid_name", "Give the library a name.")
			return
		}
		pos, ok := startFrom[strings.ToLower(strings.TrimSpace(in.StartFrom))]
		if in.StartFrom == "" {
			pos, ok = startFrom["balanced"], true
		}
		if !ok {
			apiError(w, http.StatusBadRequest, "invalid_start_from", "startFrom must be smallest, balanced or better.")
			return
		}
		path, ok := libraryPath(w, store, in.Path, 0)
		if !ok {
			return
		}
		l, err := store.AddLibrary(name, path, rules.SliderTemplate(pos), false) // not started: nothing scanned or queued
		if err != nil {
			storeErr(w, err)
			return
		}
		w.Header().Set("Location", "/api/libraries/"+strconv.FormatInt(l.ID, 10))
		oneStatus(w, http.StatusCreated, l)
	})

	api.HandleFunc("GET /api/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		l, err := store.GetLibrary(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, l)
	})

	api.HandleFunc("PATCH /api/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		var in struct{ Name, Path *string }
		if !readJSON(w, r, &in) {
			return
		}
		if _, err := store.GetLibrary(id); err != nil {
			storeErr(w, err)
			return
		}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" {
				apiError(w, http.StatusBadRequest, "invalid_name", "Give the library a name.")
				return
			}
			in.Name = &n
		}
		if in.Path != nil {
			p, ok := libraryPath(w, store, *in.Path, id)
			if !ok {
				return
			}
			in.Path = &p
		}
		l, err := store.UpdateLibrary(id, in.Name, in.Path)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, l)
	})

	// DELETE removes the record only: no media file is touched, queued jobs are cancelled, a running
	// one is stopped (original untouched, temp files removed), the flow stays in Flows, finished jobs
	// stay in the activity lists. A conversion already inside its swap (milliseconds, already checked)
	// cannot be stopped without a half swap, so the jobs are marked and stopped first and the record
	// goes only once none of them runs: a swap in progress finishes before the answer. If one has not
	// ended within libraryStopWait, nothing is removed (409).
	api.HandleFunc("DELETE /api/libraries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		// From here no new job is added or claimed for it (MarkLibraryStop), so once DeleteLibrary finds
		// none running (it checks and removes in one transaction) none can start.
		wasStarted, running, err := store.MarkLibraryStop(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		for _, j := range running {
			if eng.StopJob != nil {
				eng.StopJob(j)
			}
		}
		for deadline := time.Now().Add(libraryStopWait); ; time.Sleep(25 * time.Millisecond) {
			err := store.DeleteLibrary(id)
			switch {
			case err == nil:
				writeJSON(w, map[string]bool{"ok": true})
				return
			case !errors.Is(err, jobs.ErrLibraryBusy):
				storeErr(w, err)
				return
			case time.Now().After(deadline):
				// Undone: the marks go (a worker that already stopped puts its job back in the queue,
				// see RunOne) and the library takes new jobs again if it did.
				if err := store.ClearLibraryStop(id, wasStarted); err != nil {
					serverError(w, err)
					return
				}
				apiError(w, http.StatusConflict, "busy", "A file in this library is just being finished; try again in a moment. Nothing was removed.")
				return
			}
		}
	})

	api.HandleFunc("POST /api/libraries/{id}/scan", func(w http.ResponseWriter, r *http.Request) {
		id, ok := libID(w, r)
		if !ok {
			return
		}
		if err := store.StartLibrary(id); err != nil {
			storeErr(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow()
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	api.HandleFunc("GET /api/fs/dirs", fsDirs)
}

// libraryPath checks a folder for a library: absolute, an existing folder, and neither inside nor
// around another library's folder (their files would belong to two libraries). except is the
// library being changed.
func libraryPath(w http.ResponseWriter, store *jobs.Store, raw string, except int64) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		apiError(w, http.StatusBadRequest, "invalid_path", "Choose a folder by its full path.")
		return "", false
	}
	st, err := os.Stat(raw)
	if err != nil || !st.IsDir() {
		apiError(w, http.StatusBadRequest, "not_a_folder", "That folder does not exist, or the program cannot see it.")
		return "", false
	}
	p, err := jobs.RealPath(raw) // a link to a folder is stored as the folder it points to
	if err != nil {
		apiError(w, http.StatusBadRequest, "not_a_folder", "That folder cannot be opened: "+err.Error())
		return "", false
	}
	l, same, clash, err := store.FolderClash(p, except)
	switch {
	case err != nil:
		serverError(w, err)
		return "", false
	case clash && same:
		apiError(w, http.StatusConflict, "path_taken", "Another library already uses this folder: "+l.Name+".")
		return "", false
	case clash:
		apiError(w, http.StatusConflict, "path_overlaps", "This folder is inside, or contains, the folder of library "+l.Name+". Choose a folder that doesn't overlap.")
		return "", false
	}
	return p, true
}

// fsDirs lists the folders in one folder, for choosing a library's folder: names of folders only (no
// files, no sizes), only what the program's user can read. Without path: the top level ("/", or the
// drive letters on Windows).
func fsDirs(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		if runtime.GOOS == "windows" {
			var drives []string
			for c := 'A'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if _, err := os.Stat(d); err == nil {
					drives = append(drives, d)
				}
			}
			writeJSON(w, map[string]any{"path": "", "parent": nil, "dirs": drives})
			return
		}
		raw = "/"
	}
	if !filepath.IsAbs(raw) {
		apiError(w, http.StatusBadRequest, "invalid_path", "Give a full path.")
		return
	}
	p := filepath.Clean(raw)                              // removes "..": the answer is always about the folder actually listed
	if st, err := os.Stat(p); err == nil && !st.IsDir() { // Windows reads a file as "path not found"
		apiError(w, http.StatusBadRequest, "not_a_folder", "That is not a folder the program can open.")
		return
	}
	entries, err := os.ReadDir(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		apiError(w, http.StatusNotFound, "not_found", "That folder does not exist.")
		return
	case errors.Is(err, fs.ErrPermission):
		apiError(w, http.StatusForbidden, "not_readable", "The program is not allowed to look inside that folder.")
		return
	case err != nil:
		apiError(w, http.StatusBadRequest, "not_a_folder", "That is not a folder the program can open.")
		return
	}
	dirs := []string{}
	for _, e := range entries {
		isDir := e.IsDir()
		if !isDir && e.Type()&fs.ModeSymlink != 0 { // a link to a folder counts as a folder
			st, err := os.Stat(filepath.Join(p, e.Name()))
			isDir = err == nil && st.IsDir()
		}
		if isDir {
			dirs = append(dirs, e.Name())
		}
	}
	var parent any
	if d := filepath.Dir(p); d != p {
		parent = d
	} else if runtime.GOOS == "windows" {
		parent = "" // from a drive root, up is the drive list
	}
	writeJSON(w, map[string]any{"path": p, "parent": parent, "dirs": dirs})
}

// readJSON decodes a small JSON body; a bad one is answered with 400 and false.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	return true
}

func serverError(w http.ResponseWriter, err error) {
	apiError(w, http.StatusInternalServerError, "server_error", "Something went wrong on the server; nothing was changed. "+err.Error())
}
