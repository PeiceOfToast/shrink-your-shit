// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// flowItem is a flow as GET /api/flows shows it (UI-API-CONTRACT.md "Flows").
type flowItem struct {
	ID        int64    `json:"id,string"` // ids are opaque strings in the API
	Name      string   `json:"name"`
	UsedBy    []string `json:"usedBy"`
	MadeWith  string   `json:"madeWith"` // slider|selectors|builder, or legacy (made from program flags)
	ChangedAt string   `json:"changedAt"`
	// Kind is the flow's control level (basic|standard|advanced|earlier) and Tag design's B4 words for
	// it, built here (the control-level slice).
	Kind string `json:"kind"`
	Tag  string `json:"tag"`
}

func madeWith(kind string) string {
	switch kind {
	case rules.KindSlider, rules.KindSelectors, rules.KindLegacy:
		return kind
	}
	return "builder"
}

func flowRoutes(api *http.ServeMux, store *jobs.Store, ss *settingsStore, eng Engine) {
	view := func(f jobs.Flow, usedBy []string) flowItem {
		if usedBy == nil {
			usedBy = []string{}
		}
		return flowItem{ID: f.ID, Name: f.Name, UsedBy: usedBy, MadeWith: madeWith(f.Template.Kind), ChangedAt: f.Changed,
			Kind: f.Template.Level(), Tag: f.Template.Tag()}
	}
	flowID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such flow.")
			return 0, false
		}
		return id, true
	}
	storeErr := func(w http.ResponseWriter, err error) {
		var inUse jobs.ErrFlowInUse
		switch {
		case errors.As(err, &inUse):
			writeErrorWith(w, http.StatusConflict, "flow_in_use",
				"Libraries use this flow ("+strings.Join(inUse.UsedBy, ", ")+"), so it can't be deleted. Nothing was changed.",
				map[string]any{"usedBy": inUse.UsedBy})
		case errors.Is(err, jobs.ErrNotPublished): // review acace8e: never a library on a placeholder
			apiError(w, http.StatusConflict, "workflow_not_published", "This workflow hasn't been published yet, so nothing can use it. Publish it first. Nothing was changed.")
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such flow.")
		case errors.Is(err, jobs.ErrNameTaken):
			apiError(w, http.StatusConflict, "flow_name_taken", "Another flow already has this name.")
		default:
			serverError(w, err)
		}
	}
	readName := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		var in struct{ Name string }
		if !readJSON(w, r, &in) {
			return "", false
		}
		n := strings.TrimSpace(in.Name)
		if n == "" {
			apiError(w, http.StatusBadRequest, "invalid_name", "Give the flow a name.")
			return "", false
		}
		return n, true
	}
	one := func(w http.ResponseWriter, status int, f jobs.Flow) {
		all, err := store.Flows()
		if err != nil {
			serverError(w, err)
			return
		}
		for _, fu := range all {
			if fu.ID == f.ID {
				writeJSONStatus(w, status, view(fu.Flow, fu.UsedBy))
				return
			}
		}
		storeErr(w, jobs.ErrNotFound)
	}

	api.HandleFunc("GET /api/flows", func(w http.ResponseWriter, r *http.Request) {
		all, err := store.Flows()
		if err != nil {
			serverError(w, err)
			return
		}
		items := []flowItem{}
		for _, fu := range all {
			items = append(items, view(fu.Flow, fu.UsedBy))
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})
	// The flow's steps in plain words (setting.go flowSteps).
	api.HandleFunc("GET /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		f, err := store.GetFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"id": strconv.FormatInt(f.ID, 10), "name": f.Name, "madeWith": madeWith(f.Template.Kind),
			"kind": f.Template.Level(), "tag": f.Template.Tag(), "steps": flowSteps(f.Template)})
	})
	// New flow of a kind (control level per flow): basic is Balanced with stereo AAC, standard the
	// selectors at Balanced; the name defaults to "Basic flow" / "Standard flow" (" 2"... on a clash). No
	// kind: Basic, as before.
	api.HandleFunc("POST /api/flows", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Kind string }
		if !readJSON(w, r, &in) {
			return
		}
		t, base := rules.BasicTemplate(3, ""), "Basic flow"
		switch in.Kind {
		case "", "basic":
		case "standard":
			t, base = rules.DefaultSelectors(3).Template(), "Standard flow"
		default:
			apiError(w, http.StatusBadRequest, "invalid", "kind must be basic or standard. Nothing was changed.")
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			n, err := store.FreeFlowName(base)
			if err != nil {
				serverError(w, err)
				return
			}
			name = n
		}
		f, err := store.AddFlow(name, t)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusCreated, f)
	})
	api.HandleFunc("PATCH /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		name, ok := readName(w, r)
		if !ok {
			return
		}
		f, err := store.RenameFlow(id, name)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusOK, f)
	})
	api.HandleFunc("POST /api/flows/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		f, err := store.DuplicateFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		one(w, http.StatusCreated, f)
	})
	api.HandleFunc("DELETE /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		if err := store.DeleteFlow(id); err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	// Export writes the workflow's draft (the version being edited) as a portable file; import saves the file as a
	// new draft, never over a workflow and never published (workflow architecture 3.2 and 3.3).
	api.HandleFunc("GET /api/flows/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		id, ok := flowID(w, r)
		if !ok {
			return
		}
		f, err := store.GetFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		d, _, err := store.Draft(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="workflow-%d.json"`, id))
		writeJSON(w, workflow.Export(f.Name, d.Doc, d.Layout, eng.Version, time.Now()))
	})
	api.HandleFunc("POST /api/flows/import", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			apiError(w, http.StatusRequestEntityTooLarge, "too_large", "The file is too large (at most 8 MiB). Nothing was changed.")
			return
		}
		sc, err := workflowScopes(store)
		if err != nil {
			serverError(w, err)
			return
		}
		imp, err := workflow.ReadPortable(raw, sc.Global)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		name := imp.Name
		if name == "" {
			name = "Imported workflow"
		}
		f, err := addImported(store, name, imp.Doc, imp.Layout)
		if err != nil {
			storeErr(w, err)
			return
		}
		if len(imp.Layout.Nodes) > 0 || len(imp.Layout.Groups) > 0 {
			d, _, err := store.Draft(f.ID)
			if err == nil {
				_, err = store.SaveDraft(f.ID, imp.Doc, imp.Layout, d.Etag)
			}
			if err != nil {
				serverError(w, err)
				return
			}
		}
		v, err := workflowView(store, f.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		sc.LeaveFile = true // as a publish checks it: file references stay references until a job runs
		v["import"] = map[string]any{"missingGlobals": nonNil(imp.MissingGlobals), "unknownSteps": nonNil(imp.UnknownSteps),
			"issues": workflow.ValidateWith(imp.Doc, sc).Issues}
		writeJSONStatus(w, http.StatusCreated, v)
	})

	// Choosing another flow for a library (each library picks a flow; the control-level slice dropped the
	// "a flow per library" setting).
	api.HandleFunc("PUT /api/libraries/{id}/flow", func(w http.ResponseWriter, r *http.Request) {
		libID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || libID <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		}
		var in struct{ FlowID json.RawMessage }
		if !readJSON(w, r, &in) {
			return
		}
		fid, err := strconv.ParseInt(strings.Trim(string(in.FlowID), `"`), 10, 64) // the page sends it as a string
		if err != nil || fid < 0 {
			apiError(w, http.StatusBadRequest, "invalid_flow", "flowId must be a flow's id.")
			return
		}
		l, err := store.GetLibrary(libID)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such library.")
			return
		}
		if fid == 0 { // removing a workflow is out of scope: only assigning one is offered here
			if l.FlowID != 0 {
				apiError(w, http.StatusBadRequest, "workflow_required", "Removing a library's workflow isn't possible yet. Nothing was changed.")
				return
			}
			writeJSON(w, map[string]bool{"ok": true}) // it already has none
			return
		}
		if folderRefused(w, store, fid, l) {
			return
		}
		if _, err := store.SetLibraryFlow(libID, fid); err != nil {
			storeErr(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow() // its files are looked at again with the new flow
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}

// addImported saves an imported workflow under name, or under the next free "(imported)" name when the name is
// taken: an import never overwrites a workflow.
func addImported(store *jobs.Store, name string, doc workflow.Doc, layout workflow.Layout) (jobs.Flow, error) {
	candidate := name
	for n := 1; ; n++ {
		f, err := store.AddWorkflowWithLayout(candidate, doc, layout)
		if err == nil || !errors.Is(err, jobs.ErrNameTaken) || n > 50 {
			return f, err
		}
		candidate = fmt.Sprintf("%s (imported %d)", name, n)
		if n == 1 {
			candidate = name + " (imported)"
		}
	}
}

// nonNil is s, or an empty list when s is nil, so the JSON shows [] rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
