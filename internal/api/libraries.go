// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// The libraries group (page 09): the list, one library, Add, the panel's Save and Remove, and the two
// scans. A scan runs through the library package and its new files go to the job queue; the answer
// never waits for the scan.

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
		if !writeLibraryFolderError(w, err) {
			if errors.Is(err, library.ErrInvalidWatch) {
				writeError(w, http.StatusBadRequest, "invalid_request", "Pick hourly, daily or weekly.", "watch", 0)
			} else {
				internalError(w)
			}
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

// libraryPatchInput is page 09's Save: only the changed fields are sent. workflowId is raw JSON
// because absent, null and an id mean three different things: absent leaves it alone, null takes the
// workflow away, and an id sets it.
type libraryPatchInput struct {
	Name       *string         `json:"name"`
	Path       *string         `json:"path"`
	WorkflowID json.RawMessage `json:"workflowId"`
	Watch      *string         `json:"watch"`
	Paused     *bool           `json:"paused"`
}

// patchLibrary is PATCH /libraries/{id}: one Save of the library panel. The whole body is checked
// before anything is saved, and a saved change puts library.changed on the log and the live stream.
// Giving a needs_workflow library its workflow queues the files it already counted, with no rescan.
func (s *Server) patchLibrary(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		return
	}
	var body libraryPatchInput
	if !decode(w, r, &body) {
		return
	}
	lib, err := rs.Catalog.GetLibrary(r.Context(), id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}

	var patch library.Patch
	setWorkflow := false
	if body.Name != nil {
		patch.Name = body.Name
	}
	if body.Path != nil {
		patch.Path = body.Path
	}
	if body.Watch != nil {
		// Add reads an empty watch as its default; a Save names one of the three (page 09).
		watch := library.Watch(strings.ToLower(strings.TrimSpace(*body.Watch)))
		if watch != library.WatchHourly && watch != library.WatchDaily && watch != library.WatchWeekly {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Pick hourly, daily or weekly.", "watch", 0)
			return
		}
		patch.Watch = &watch
	}
	if body.Paused != nil {
		patch.Paused = body.Paused
	}
	if body.WorkflowID != nil {
		if bytes.Equal(bytes.TrimSpace(body.WorkflowID), []byte("null")) {
			patch.ClearWorkflow = true
		} else {
			wid, ok := workflowIDFromJSON(body.WorkflowID)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid_request", "Send the workflow's id, or null.", "workflowId", 0)
				return
			}
			if _, err := rs.Workflows.Get(r.Context(), wid); err != nil {
				if errors.Is(err, flow.ErrNotFound) {
					writeError(w, http.StatusBadRequest, "workflow_not_found", "That workflow doesn't exist.", "workflowId", 0)
				} else {
					internalError(w)
				}
				return
			}
			patch.WorkflowID = &wid
			setWorkflow = true
		}
	}

	// A library whose workflow is null, or names one that was deleted, is needing one (page 09).
	hadWorkflow := false
	if lib.WorkflowID != nil {
		switch _, err := rs.Workflows.Get(r.Context(), *lib.WorkflowID); {
		case err == nil:
			hadWorkflow = true
		case !errors.Is(err, flow.ErrNotFound):
			internalError(w)
			return
		}
	}

	// A path change takes the library's waiting jobs with it, in the same transaction as the rows
	// they name (review-890 path change); a running one finishes.
	var cancelledWaiting []jobs.CancelledWaiting
	upd, err := rs.Catalog.UpdateLibraryWith(r.Context(), id, patch, func(tx *sql.Tx) error {
		if rs.Jobs == nil {
			return nil
		}
		var cerr error
		cancelledWaiting, cerr = rs.Jobs.CancelWaitingIn(tx, id)
		return cerr
	})
	if err != nil {
		switch {
		case errors.Is(err, library.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		case errors.Is(err, library.ErrInvalidWatch):
			writeError(w, http.StatusBadRequest, "invalid_setting", "Pick hourly, daily or weekly.", "watch", 0)
		default:
			if !writeLibraryFolderError(w, err) {
				internalError(w)
			}
		}
		return
	}

	if len(cancelledWaiting) > 0 {
		if err := rs.Jobs.CancelledWaitingHistory(cancelledWaiting, id, pathChangedCancellation); err != nil {
			internalError(w)
			return
		}
	}

	// A needs_workflow library that is given its workflow starts queueing the files it already
	// counted: the same Decide a scan asks, over the catalog's rows, with no walk and no rescan.
	if setWorkflow && !hadWorkflow && rs.Scanner != nil {
		if _, err := rs.Scanner.QueueFiles(r.Context(), upd); err != nil {
			// The save stands and the next scan queues the files; the log says what happened.
			if rs.Events != nil {
				_ = rs.Events.Emit(events.Event{
					Level: events.LevelWarn, Kind: events.KindWarning, Source: "library",
					Code:      "library.queue_failed",
					LibraryID: strconv.FormatInt(upd.ID, 10),
					Message:   "Could not queue the files already counted: " + err.Error() + ". The next scan queues them.",
				})
			}
		}
	}

	counts, err := rs.Catalog.CountFiles(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	row, err := s.libraryRowOf(r, upd, counts[upd.ID])
	if err != nil {
		internalError(w)
		return
	}
	if libraryChanged(lib, upd) {
		s.libraryChangedEvent(rs, upd.ID, "Changed "+upd.Name)
	}
	writeJSON(w, http.StatusOK, row)
}

// pathChangedCancellation is the History line a path change's cancelled jobs get: their file is
// under a folder the library no longer watches, so nothing will run them.
const pathChangedCancellation = "The library's folder changed, so this file is no longer in it."

// deleteLibrary is DELETE /libraries/{id}: Remove with its confirm. Waiting jobs for the library are
// cancelled and a running one finishes; files on disk, converted or original, are not touched. The
// library's catalog rows go with it.
func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
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
	if rs.Jobs != nil {
		if _, err := rs.Jobs.CancelWaitingFor(r.Context(), id); err != nil {
			internalError(w)
			return
		}
	}
	if err := rs.Catalog.DeleteLibrary(r.Context(), id); err != nil {
		writeLibraryError(w, err)
		return
	}
	s.libraryChangedEvent(rs, id, "Removed "+lib.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// libraryChanged reports whether a Save changed any of the fields the pages show.
func libraryChanged(before, after library.Library) bool {
	if before.Name != after.Name || before.Path != after.Path || before.Watch != after.Watch || before.Paused != after.Paused {
		return true
	}
	return (before.WorkflowID == nil) != (after.WorkflowID == nil) ||
		(before.WorkflowID != nil && *before.WorkflowID != *after.WorkflowID)
}

// libraryChangedEvent puts one library.changed on the log and the live stream, so every open page
// refetches its libraries.
func (s *Server) libraryChangedEvent(rs *Deps, id int64, message string) {
	if rs.Events == nil {
		return
	}
	_ = rs.Events.Emit(events.Event{
		Level: events.LevelInfo, Kind: events.KindLibraryChanged, Source: "library",
		LibraryID: strconv.FormatInt(id, 10), Message: message,
	})
}

// workflowIDFromJSON reads a workflow id sent as a JSON string (types.ts's shape) or a number.
func workflowIDFromJSON(raw []byte) (int64, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		return id, err == nil && id > 0
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		id, err := n.Int64()
		return id, err == nil && id > 0
	}
	return 0, false
}

// writeLibraryFolderError answers a folder refusal with Add's own words, and answers whether it did.
// Add and a path change both come through here, so Save refuses a folder exactly as Add does.
func writeLibraryFolderError(w http.ResponseWriter, err error) bool {
	var pathErr *library.PathError
	switch {
	case errors.As(err, &pathErr):
		writeError(w, http.StatusBadRequest, "invalid_request", pathErr.Error(), "path", 0)
	case errors.Is(err, library.ErrPathAlreadyWatched):
		writeError(w, http.StatusBadRequest, "invalid_request", "Another library already watches this folder. Choose a different folder.", "path", 0)
	case errors.Is(err, library.ErrPathNested):
		var nested *library.NestedError
		errors.As(err, &nested)
		message := "This folder overlaps another library's folder. Choose a folder of its own."
		if nested != nil {
			message = nested.Sentence()
		}
		writeError(w, http.StatusBadRequest, "invalid_request", message, "path", 0)
	default:
		return false
	}
	return true
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
