// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// libraryPreviewJSON is "With this workflow: N of M would be converted" (page 09).
type libraryPreviewJSON struct {
	WouldConvert int           `json:"wouldConvert"`
	Total        int           `json:"total"`
	Skipped      []previewSkip `json:"skipped"`
}

type previewSkip struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// getLibraryPreview is GET /api/v2/libraries/{id}/preview?workflowId=: what the workflow would do to the library's files,
// decided from each file's stored probe by the same decision the queue uses. It queues nothing and writes nothing.
func (s *Server) getLibraryPreview(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	ctx := r.Context()
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That library doesn't exist.", "", 0)
		return
	}
	if _, err := rs.Catalog.GetLibrary(ctx, id); err != nil {
		writeLibraryError(w, err)
		return
	}
	workflowID, err := strconv.ParseInt(r.URL.Query().Get("workflowId"), 10, 64)
	if err != nil || workflowID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_setting", "Pick a workflow.", "workflowId", 0)
		return
	}
	wf, err := rs.Workflows.Get(ctx, workflowID)
	if errors.Is(err, flow.ErrNotFound) {
		writeError(w, http.StatusNotFound, "workflow_not_found", "That workflow doesn't exist.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	files, err := rs.Catalog.Files(ctx, id)
	if err != nil {
		internalError(w)
		return
	}
	// The decision is the one the queue makes for a file under the workflow's published revision. A workflow with no
	// published revision, or one that cannot be read, decides nothing: every file says so.
	var rev flow.Revision
	haveRev := false
	if wf.CurrentRev > 0 {
		if rev, err = rs.Workflows.Revision(ctx, workflowID, wf.CurrentRev); err == nil {
			haveRev = true
		}
	}
	decide := func(_ library.File, info media.Info) (bool, string) {
		if !haveRev {
			return false, "workflow_missing"
		}
		return flow.DecideFile(rev, info)
	}
	p := library.PreviewOf(files, decide)
	out := libraryPreviewJSON{WouldConvert: p.WouldConvert, Total: p.Total, Skipped: make([]previewSkip, 0, len(p.Skipped))}
	for _, sk := range p.Skipped {
		out.Skipped = append(out.Skipped, previewSkip{Reason: sk.Reason, Count: sk.Count})
	}
	writeJSON(w, http.StatusOK, out)
}
