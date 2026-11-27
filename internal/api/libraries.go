// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// The libraries group (page 09): the list, one library, Add, and the two scans. A scan runs through
// the library package and its new files go to the job queue; the answer never waits for the scan.

// libraryRow is page 09's Library. Its ids are decimal strings, as types.ts has them.
type libraryRow struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	ShareID    *string      `json:"shareId,omitempty"`
	Workflow   *workflowRef `json:"workflow"`
	Files      int          `json:"files"`
	Waiting    int          `json:"waiting"`
	SavedBytes int64        `json:"savedBytes"`
	SavedPct   float64      `json:"savedPct"`
	LastScan   *lastScanRow `json:"lastScan"`
	Status     string       `json:"status"`
	Watch      string       `json:"watch"`
	Paused     bool         `json:"paused"`
	NextScanAt *string      `json:"nextScanAt"`
}

type workflowRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lastScanRow struct {
	At      string `json:"at"`
	Found   int    `json:"found"`
	New     int    `json:"new"`
	Changed int    `json:"changed"`
}

func (s *Server) getLibraries(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	libs, err := rs.Catalog.ListLibraries(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	counts, err := rs.Catalog.CountFiles(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	out := []libraryRow{}
	for _, lib := range libs {
		row, err := s.libraryRowOf(r, lib, counts[lib.ID])
		if err != nil {
			internalError(w)
			return
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getLibrary(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		return
	}
	lib, err := rs.Catalog.GetLibrary(r.Context(), id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	counts, err := rs.Catalog.CountFiles(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	row, err := s.libraryRowOf(r, lib, counts[lib.ID])
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// libraryInput is page 09's Add: the folder, a name, and the watch. Shares are not in stage 1.
type libraryInput struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	ShareID string `json:"shareId"`
	Watch   string `json:"watch"`
}

func (s *Server) postLibrary(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body libraryInput
	if !decode(w, r, &body) {
		return
	}
	if body.ShareID != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Shares aren't available yet. Choose a folder on this computer.", "shareId", 0)
		return
	}
	lib, err := rs.Catalog.AddLibrary(r.Context(), library.NewLibrary{
		Name: body.Name, Path: body.Path, Watch: library.Watch(body.Watch),
	})
	if err != nil {
		var pathErr *library.PathError
		switch {
		case errors.As(err, &pathErr):
			writeError(w, http.StatusBadRequest, "invalid_request", pathErr.Error(), "path", 0)
		case errors.Is(err, library.ErrPathAlreadyWatched):
			writeError(w, http.StatusBadRequest, "invalid_request", "Another library already watches this folder. Choose a different folder.", "path", 0)
		case errors.Is(err, library.ErrInvalidWatch):
			writeError(w, http.StatusBadRequest, "invalid_request", "Pick hourly, daily or weekly.", "watch", 0)
		default:
			internalError(w)
		}
		return
	}
	counts, err := rs.Catalog.CountFiles(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	row, err := s.libraryRowOf(r, lib, counts[lib.ID])
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) scanLibrary(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		return
	}
	lib, err := rs.Catalog.GetLibrary(r.Context(), id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	startScan(rs, lib)
	counts, err := rs.Catalog.CountFiles(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	row, err := s.libraryRowOf(r, lib, counts[lib.ID])
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) scanAllLibraries(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	libs, err := rs.Catalog.ListLibraries(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	for _, lib := range libs {
		if lib.Paused {
			continue // a paused library is not scanned, even by hand
		}
		startScan(rs, lib)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// libraryRowOf is one library in page 09's shape: its catalogued files, its queue counts and the
// workflow it uses.
func (s *Server) libraryRowOf(r *http.Request, lib library.Library, files int) (libraryRow, error) {
	rs := s.ready.Load()
	row := libraryRow{
		ID:       strconv.FormatInt(lib.ID, 10),
		Name:     lib.Name,
		Path:     lib.Path,
		Files:    files,
		Watch:    string(lib.Watch),
		Paused:   lib.Paused,
		Status:   "watching",
		LastScan: nil,
	}
	if lib.ShareID != nil {
		share := strconv.FormatInt(*lib.ShareID, 10)
		row.ShareID = &share
	}
	if lib.NextScanAt != nil {
		next := rfc3339(*lib.NextScanAt)
		row.NextScanAt = &next
	}
	if lib.LastScan != nil {
		row.LastScan = &lastScanRow{
			At: rfc3339(lib.LastScan.At), Found: lib.LastScan.Found,
			New: lib.LastScan.New, Changed: lib.LastScan.Changed,
		}
	}
	if lib.WorkflowID != nil {
		wf, err := rs.Workflows.Get(r.Context(), *lib.WorkflowID)
		switch {
		case err == nil:
			row.Workflow = &workflowRef{ID: strconv.FormatInt(wf.ID, 10), Name: wf.Name}
		case errors.Is(err, flow.ErrNotFound):
			// The workflow was deleted: the library needs another one.
		default:
			return libraryRow{}, err
		}
	}
	switch {
	case lib.Paused:
		row.Status = "paused"
	case row.Workflow == nil:
		row.Status = "needs_workflow"
	}
	if tally, err := rs.Jobs.Tally(r.Context(), lib.ID); err == nil {
		row.Waiting = tally.Waiting
		row.SavedBytes = tally.SavedBytes
		if tally.BeforeBytes > 0 {
			row.SavedPct = math.Round(float64(tally.SavedBytes)/float64(tally.BeforeBytes)*1000) / 10
		}
	}
	return row, nil
}

// libraryID reads the {id} part of a library route. A non-number is no library, so it answers 404.
func libraryID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func writeLibraryError(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		return
	}
	internalError(w)
}

func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
