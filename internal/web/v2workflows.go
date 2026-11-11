// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"log"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// v2Workflows are V1's /api/v2 routes (UI-API-CONTRACT-v2.md): the node registry, templates, and
// workflows with their drafts, validation, publishing and history. A published workflow runs through
// the engine as its flow always has: publishing writes the template the engine reads.
func v2Workflows(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("GET /api/v2/step-definitions", func(w http.ResponseWriter, r *http.Request) {
		defs := workflow.Definitions()
		if ft := r.URL.Query().Get("fileType"); ft != "" {
			defs = slices.DeleteFunc(defs, func(d workflow.StepDefinition) bool { return !slices.Contains(d.FileTypes, ft) })
		}
		writeJSON(w, map[string]any{"items": defs, "total": len(defs)})
	})
	api.HandleFunc("GET /api/v2/templates", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{{"id": "shrink_videos", "name": "Shrink videos", "fileType": "video",
			"description": "Converts videos to a smaller format, keeping the picture and the sound you choose.",
			"steps":       stepNames(workflow.Preset()), "changesOriginals": false}}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})

	wfID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
			return 0, false
		}
		return id, true
	}
	storeErr := func(w http.ResponseWriter, err error) {
		var inUse jobs.ErrFlowInUse
		switch {
		case errors.As(err, &inUse):
			writeErrorWith(w, http.StatusConflict, "in_use",
				"Libraries use this workflow ("+strings.Join(inUse.UsedBy, ", ")+"), so it can't be deleted. Nothing was changed.",
				map[string]any{"usedBy": inUse.UsedBy})
		case errors.Is(err, jobs.ErrNotPublished): // review acace8e: never a library on a placeholder
			apiError(w, http.StatusConflict, "workflow_not_published", "This workflow hasn't been published yet, so nothing can use it. Publish it first. Nothing was changed.")
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
		case errors.Is(err, jobs.ErrNameTaken):
			apiError(w, http.StatusConflict, "name_taken", "Another workflow already has this name. Nothing was changed.")
		case errors.Is(err, jobs.ErrStaleDraft):
			apiError(w, http.StatusConflict, "stale_draft", "This workflow was changed elsewhere since you opened it. Reload it to see the changes. Nothing was changed.")
		default:
			serverError(w, err)
		}
	}
	one := func(w http.ResponseWriter, id int64) {
		v, err := workflowView(store, id)
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, v)
	}

	api.HandleFunc("GET /api/v2/workflows", func(w http.ResponseWriter, r *http.Request) {
		ids, err := store.WorkflowIDs()
		if err != nil {
			serverError(w, err)
			return
		}
		q := r.URL.Query()
		items := []map[string]any{}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		for _, id := range ids {
			v, err := workflowViewWith(store, id, libs)
			if err != nil { // one that can't be read is left out, so the others still list (review ae3a28f)
				log.Printf("workflow %d left out of the list: %v", id, err)
				continue
			}
			if keepWorkflow(v, q) {
				items = append(items, v)
			}
		}
		sortItems(items, q.Get("sort"), "name")
		page, ok := pageOfItems(w, q, items)
		if !ok {
			return
		}
		writeJSON(w, map[string]any{"items": page, "total": len(items)})
	})
	api.HandleFunc("GET /api/v2/workflows/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := wfID(w, r); ok {
			one(w, id)
		}
	})
	api.HandleFunc("POST /api/v2/workflows", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, FileType, TemplateID string }
		if !readJSON(w, r, &in) {
			return
		}
		name := strings.TrimSpace(in.Name)
		switch {
		case name == "":
			apiError(w, http.StatusBadRequest, "invalid_name", "Give the workflow a name. Nothing was changed.")
			return
		case in.FileType != "" && in.FileType != "video":
			apiError(w, http.StatusBadRequest, "invalid", "This version makes video workflows. Nothing was changed.")
			return
		case in.TemplateID != "" && in.TemplateID != "shrink_videos":
			apiError(w, http.StatusBadRequest, "invalid", "There is no such template. Nothing was changed.")
			return
		}
		f, err := store.AddWorkflow(name, workflow.Preset())
		if err != nil {
			storeErr(w, err)
			return
		}
		v, err := workflowView(store, f.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, v)
	})
	api.HandleFunc("PATCH /api/v2/workflows/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		var in struct {
			Name, Description, Status *string
			Favourite                 *bool
			Tags                      []string
		}
		if !readJSON(w, r, &in) {
			return
		}
		if in.Status != nil && *in.Status != "enabled" && *in.Status != "disabled" {
			apiError(w, http.StatusBadRequest, "invalid", "status is enabled or disabled. Nothing was changed.")
			return
		}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" {
				apiError(w, http.StatusBadRequest, "invalid_name", "Give the workflow a name. Nothing was changed.")
				return
			}
			if _, err := store.RenameFlow(id, n); err != nil {
				storeErr(w, err)
				return
			}
		}
		if err := store.SetWorkflowMeta(id, in.Description, in.Status, in.Favourite, in.Tags); err != nil {
			storeErr(w, err)
			return
		}
		one(w, id)
	})
	api.HandleFunc("DELETE /api/v2/workflows/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := wfID(w, r); ok {
			if err := store.DeleteFlow(id); err != nil {
				storeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	api.HandleFunc("POST /api/v2/workflows/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := wfID(w, r); ok {
			f, err := store.DuplicateFlow(id)
			if err != nil {
				storeErr(w, err)
				return
			}
			v, err := workflowView(store, f.ID)
			if err != nil {
				serverError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, v)
		}
	})

	api.HandleFunc("GET /api/v2/workflows/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		if _, err := store.EnsureRevision(id); err != nil {
			storeErr(w, err)
			return
		}
		d, _, err := store.Draft(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, revisionView(d))
	})
	api.HandleFunc("PUT /api/v2/workflows/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		var in struct {
			Workflow workflow.Doc
			Layout   workflow.Layout
		}
		if !readJSON(w, r, &in) {
			return
		}
		if _, err := store.EnsureRevision(id); err != nil {
			storeErr(w, err)
			return
		}
		d, err := store.SaveDraft(id, in.Workflow, in.Layout, r.Header.Get("If-Match"))
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, revisionView(d))
	})
	api.HandleFunc("POST /api/v2/workflows/{id}/validate", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := wfID(w, r); !ok {
			return
		}
		var in struct{ Workflow workflow.Doc }
		if !readJSON(w, r, &in) {
			return
		}
		writeJSON(w, workflow.Validate(in.Workflow))
	})
	api.HandleFunc("GET /api/v2/workflows/{id}/publish-preview", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		d, fresh, err := store.Draft(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		latest, has, err := store.LatestRevision(id)
		if err != nil {
			serverError(w, err)
			return
		}
		next := 1
		var before *workflow.Doc
		if has {
			next, before = *latest.Number+1, &latest.Doc
		}
		changes := []map[string]string{}
		if !fresh {
			changes = docChanges(before, d.Doc)
		}
		v, err := workflowView(store, id)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"nextNumber": next, "changes": changes, "libraries": v["libraries"],
			"changesOriginals": commitPolicy(d.Doc) == "replace", "validation": workflow.Validate(d.Doc).Issues})
	})
	api.HandleFunc("POST /api/v2/workflows/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		var in struct{ Etag string }
		if !readJSON(w, r, &in) {
			return
		}
		d, fresh, err := store.Draft(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		if fresh || d.Etag != in.Etag {
			storeErr(w, jobs.ErrStaleDraft)
			return
		}
		if res := workflow.Validate(d.Doc); res.Errors() {
			writeErrorWith(w, http.StatusBadRequest, "has_errors", "This workflow has problems to fix before it can be published. Nothing was changed.",
				map[string]any{"issues": res.Issues})
			return
		}
		f, err := store.GetFlow(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		t, issues := workflow.ToTemplate(d.Doc, f.Template)
		if len(issues) > 0 {
			writeErrorWith(w, http.StatusBadRequest, "has_errors", issues[0].Message+" Nothing was changed.", map[string]any{"issues": issues})
			return
		}
		if slices.Contains(stepTypes(d.Doc), workflow.TypeLegacy) { // its settings are the server's: only ones published before
			known, err := store.KnownTemplate(id, t)
			if err != nil {
				serverError(w, err)
				return
			}
			if !known {
				apiError(w, http.StatusBadRequest, "invalid_config", "The earlier install's settings can't be changed here. To change them, start from the Shrink videos steps. Nothing was changed.")
				return
			}
		}
		if !outputUsable(w, store, id, t) { // the same folder checks as the old Save (#103: system_folder, inside_library, ...)
			return
		}
		v, err := store.Publish(id, in.Etag, t)
		if err != nil {
			storeErr(w, err)
			return
		}
		if eng.ScanNow != nil {
			eng.ScanNow() // its libraries' files are looked at again, as after the old Save (review acace8e)
		}
		writeJSON(w, revisionView(v))
	})
	api.HandleFunc("GET /api/v2/workflows/{id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		id, ok := wfID(w, r)
		if !ok {
			return
		}
		if _, err := store.EnsureRevision(id); err != nil {
			storeErr(w, err)
			return
		}
		revs, err := store.Revisions(id)
		if err != nil {
			serverError(w, err)
			return
		}
		items := []map[string]any{}
		for _, v := range revs {
			items = append(items, map[string]any{"id": strconv.FormatInt(v.ID, 10), "number": v.Number, "publishedAt": v.Published, "logicHash": v.LogicHash})
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	})
	revision := func(w http.ResponseWriter, r *http.Request) (int64, jobs.Revision, bool) {
		id, ok := wfID(w, r)
		if !ok {
			return 0, jobs.Revision{}, false
		}
		rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such version.")
			return 0, jobs.Revision{}, false
		}
		v, err := store.RevisionByID(id, rev)
		if errors.Is(err, jobs.ErrNotFound) {
			apiError(w, http.StatusNotFound, "not_found", "There is no such version.")
			return 0, v, false
		}
		if err != nil {
			serverError(w, err)
			return 0, v, false
		}
		return id, v, true
	}
	api.HandleFunc("GET /api/v2/workflows/{id}/revisions/{rev}", func(w http.ResponseWriter, r *http.Request) {
		if _, v, ok := revision(w, r); ok {
			writeJSON(w, revisionView(v))
		}
	})
	api.HandleFunc("POST /api/v2/workflows/{id}/revisions/{rev}/restore", func(w http.ResponseWriter, r *http.Request) {
		id, v, ok := revision(w, r)
		if !ok {
			return
		}
		cur, _, err := store.Draft(id)
		if err != nil {
			storeErr(w, err)
			return
		}
		d, err := store.SaveDraft(id, v.Doc, v.Layout, cur.Etag)
		if err != nil {
			storeErr(w, err)
			return
		}
		writeJSON(w, revisionView(d))
	})
}

// workflowView is a workflow as the contract's Workflow.
func workflowView(store *jobs.Store, id int64) (map[string]any, error) {
	return workflowViewWith(store, id, nil)
}

// workflowViewWith is workflowView with the libraries already read (nil: read them), so a list reads
// them once (review 109).
func workflowViewWith(store *jobs.Store, id int64, libs []jobs.Library) (map[string]any, error) {
	latest, err := store.EnsureRevision(id)
	if err != nil {
		return nil, err
	}
	f, err := store.GetFlow(id)
	if err != nil {
		return nil, err
	}
	meta, err := store.WorkflowMetaOf(id)
	if err != nil {
		return nil, err
	}
	draft, fresh, err := store.Draft(id)
	if err != nil {
		return nil, err
	}
	if libs == nil {
		if libs, err = store.Libraries(); err != nil {
			return nil, err
		}
	}
	used := []map[string]string{}
	for _, l := range libs {
		if l.FlowID == id {
			used = append(used, map[string]string{"id": strconv.FormatInt(l.ID, 10), "name": l.Name})
		}
	}
	runs, err := store.WorkflowRunsSince(id, time.Now().Add(-7*24*time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	shown := draft.Doc // the draft, or the published revision when there's none (Draft makes it from that)
	res := workflow.Validate(shown)
	errs, warns := 0, 0
	for _, i := range res.Issues {
		if i.Severity == "error" {
			errs++
		} else {
			warns++
		}
	}
	v := map[string]any{"id": strconv.FormatInt(id, 10), "name": f.Name, "description": meta.Description, "fileType": "video",
		"status": meta.Status, "favourite": meta.Favourite, "tags": meta.Tags, "presetId": nil, "summaryTag": f.Template.Tag(),
		"publishedRevisionId": nil, "version": nil, "draftDiffers": false, "validation": map[string]int{"errors": errs, "warnings": warns},
		"libraries": used, "activeJobs": runs.Active, "createdAt": f.Created, "changedAt": f.Changed,
		"lastRun":          map[string]any{"at": nilIfEmpty(runs.LastAt), "completed": runs.Completed, "failed": runs.Failed, "skipped": 0},
		"changesOriginals": false}
	if latest.ID != 0 {
		v["publishedRevisionId"], v["version"] = strconv.FormatInt(latest.ID, 10), latest.Number
		v["draftDiffers"] = !fresh && draft.LogicHash != latest.LogicHash
		v["changesOriginals"] = commitPolicy(latest.Doc) == "replace"
		if types := stepTypes(latest.Doc); slices.Equal(types, workflow.PresetOrder) {
			v["presetId"] = "shrink_videos" // still the preset's steps (lead, Q3: detached once edited away)
		}
	} else { // never published: the same fields, from its draft (review 109)
		v["draftDiffers"] = true
		v["changesOriginals"] = commitPolicy(draft.Doc) == "replace"
		if slices.Equal(stepTypes(draft.Doc), workflow.PresetOrder) {
			v["presetId"] = "shrink_videos"
		}
		v["summaryTag"] = ""
		if t, issues := workflow.ToTemplate(draft.Doc, rules.Template{}); len(issues) == 0 {
			v["summaryTag"] = t.Tag()
		}
	}
	return v, nil
}

// revisionView is a revision as the contract's WorkflowRevision.
func revisionView(r jobs.Revision) map[string]any {
	res := workflow.Validate(r.Doc)
	id := any(nil)
	if r.ID != 0 {
		id = strconv.FormatInt(r.ID, 10)
	}
	return map[string]any{"id": id, "workflowId": strconv.FormatInt(r.WorkflowID, 10), "number": r.Number, "status": r.Status,
		"workflow": r.Doc, "layout": r.Layout, "logicHash": r.LogicHash, "stackable": res.Stackable,
		"stepSummaries": res.StepSummaries, "validation": res.Issues, "etag": r.Etag, "createdAt": r.Created, "publishedAt": r.Published}
}

func stepTypes(d workflow.Doc) []string {
	out := []string{}
	for _, n := range d.Nodes {
		out = append(out, n.Type)
	}
	return out
}

func stepNames(d workflow.Doc) []string {
	out := []string{}
	for _, n := range d.Nodes {
		if def, ok := workflow.Definition(n.Type); ok {
			out = append(out, def.Name)
		}
	}
	return out
}

func commitPolicy(d workflow.Doc) string {
	for _, n := range d.Nodes {
		if n.Type == workflow.TypeCommit {
			if p, ok := n.Config["policy"].(string); ok {
				return p
			}
		}
		if n.Type == workflow.TypeLegacy { // an earlier install's settings: replace unless beside
			if t, ok := n.Config["template"].(map[string]any); ok {
				if p, ok := t["params"].(map[string]any); ok && p["output"] == "beside" {
					return "beside"
				}
			}
			return "replace"
		}
	}
	return ""
}

// docChanges says what publishing would change, step by step (design's Publish panel).
func docChanges(before *workflow.Doc, after workflow.Doc) []map[string]string {
	out := []map[string]string{}
	old := map[string]workflow.Node{}
	if before != nil {
		for _, n := range before.Nodes {
			old[n.ID] = n
		}
	}
	summaries := workflow.Validate(after).StepSummaries
	for _, n := range after.Nodes {
		name := n.Type
		if def, ok := workflow.Definition(n.Type); ok {
			name = def.Name
		}
		o, had := old[n.ID]
		switch {
		case !had:
			out = append(out, map[string]string{"nodeId": n.ID, "kind": "added", "summary": name + ": " + summaries[n.ID]})
		case !reflect.DeepEqual(o.Config, n.Config) || o.Enabled != n.Enabled || o.Type != n.Type:
			out = append(out, map[string]string{"nodeId": n.ID, "kind": "changed", "summary": name + ": " + summaries[n.ID]})
		}
		delete(old, n.ID)
	}
	for id, n := range old {
		name := n.Type
		if def, ok := workflow.Definition(n.Type); ok {
			name = def.Name
		}
		out = append(out, map[string]string{"nodeId": id, "kind": "removed", "summary": name})
	}
	return out
}

// keepWorkflow is the list convention's filters for workflows (q, status, fileType, tag, favourite, libraryId).
func keepWorkflow(v map[string]any, q map[string][]string) bool {
	get := func(k string) []string {
		if vs := q[k]; len(vs) > 0 && vs[0] != "" {
			return strings.Split(vs[0], ",")
		}
		return nil
	}
	if s := get("q"); s != nil {
		text := strings.ToLower(v["name"].(string) + " " + v["description"].(string))
		if !strings.Contains(text, strings.ToLower(strings.Join(s, ","))) {
			return false
		}
	}
	if s := get("status"); s != nil && !slices.Contains(s, v["status"].(string)) {
		return false
	}
	if s := get("fileType"); s != nil && !slices.Contains(s, "video") {
		return false
	}
	if s := get("favourite"); s != nil && (s[0] == "true") != v["favourite"].(bool) {
		return false
	}
	if s := get("tag"); s != nil && !slices.ContainsFunc(v["tags"].([]string), func(t string) bool { return slices.Contains(s, t) }) {
		return false
	}
	if s := get("libraryId"); s != nil && !slices.ContainsFunc(v["libraries"].([]map[string]string), func(l map[string]string) bool { return slices.Contains(s, l["id"]) }) {
		return false
	}
	return true
}

// sortItems sorts by one field ("-field": descending), ties by id (the list convention).
func sortItems(items []map[string]any, by, def string) {
	if by == "" {
		by = def
	}
	desc := strings.HasPrefix(by, "-")
	by = strings.TrimPrefix(by, "-")
	key := func(m map[string]any) string {
		switch v := m[by].(type) {
		case string:
			return strings.ToLower(v)
		case int:
			return strconv.Itoa(1e9 + v)
		case *int:
			if v != nil {
				return strconv.Itoa(1e9 + *v)
			}
		case bool:
			return strconv.FormatBool(v)
		}
		return ""
	}
	id := func(m map[string]any) int { n, _ := strconv.Atoi(m["id"].(string)); return n }
	sort.SliceStable(items, func(i, j int) bool {
		a, b := key(items[i]), key(items[j])
		if a == b {
			return id(items[i]) < id(items[j])
		}
		return a < b != desc
	})
}

// pageOfItems applies limit (default 50, at most 500) and offset.
func pageOfItems(w http.ResponseWriter, q map[string][]string, items []map[string]any) ([]map[string]any, bool) {
	num := func(k string, def int) (int, bool) {
		vs := q[k]
		if len(vs) == 0 || vs[0] == "" {
			return def, true
		}
		n, err := strconv.Atoi(vs[0])
		return n, err == nil && n >= 0
	}
	limit, ok1 := num("limit", 50)
	offset, ok2 := num("offset", 0)
	if !ok1 || !ok2 || limit > 500 {
		apiError(w, http.StatusBadRequest, "invalid", "limit is 0 to 500 and offset 0 or more.")
		return nil, false
	}
	if offset > len(items) {
		offset = len(items)
	}
	return items[offset:min(offset+limit, len(items))], true
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
