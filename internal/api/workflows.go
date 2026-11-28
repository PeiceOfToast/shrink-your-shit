// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// The workflows group (pages 04 and 07): the list, create, the draft the editor loads and saves,
// publish, the revisions and restoring one, duplicate and delete. The rules are the flow package's;
// this file shapes them into the pages' JSON.

// workflowUseJSON is WorkflowUse in types.ts: the library that uses a workflow.
type workflowUseJSON struct {
	Type string `json:"type"` // "library" today
	ID   string `json:"id"`
	Name string `json:"name"`
}

// workflowRowJSON is WorkflowRow in types.ts: one row of page 04's list.
type workflowRowJSON struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Kind         string            `json:"kind"`
	UsedBy       []workflowUseJSON `json:"usedBy"`
	UpdatedAt    string            `json:"updatedAt"`
	State        string            `json:"state"`
	ProblemCount int               `json:"problemCount"`
	MadeWith     string            `json:"madeWith"`
	FitsGuided   bool              `json:"fitsGuided"`
}

// workflowSubtree is every route below /workflows/ except the starters' and the guide's literal
// ones: one workflow by id, its draft, publish, revisions, restore, duplicate and delete. It
// reads the path's own segments and hands each route the id (and rev) it would have had from the
// mux, because Go's mux cannot rank these patterns against "/workflows/starters/{id}".
func (s *Server) workflowSubtree(w http.ResponseWriter, r *http.Request) {
	segs := strings.Split(strings.Trim(r.PathValue("rest"), "/"), "/")
	id := func() { r.SetPathValue("id", segs[0]) }
	switch r.Method {
	case http.MethodGet:
		switch {
		case len(segs) == 1:
			id()
			s.getWorkflow(w, r)
		case len(segs) == 2 && segs[1] == "revisions":
			id()
			s.listWorkflowRevisions(w, r)
		case len(segs) == 2 && segs[1] == "answers":
			// "new" is not an id: the guided page asks for the answers it starts from.
			r.SetPathValue("id", segs[0])
			s.getGuideAnswers(w, r)
		default:
			notFound(w, r)
		}
	case http.MethodPut:
		switch {
		case len(segs) == 2 && segs[1] == "draft":
			id()
			s.putWorkflowDraft(w, r)
		case len(segs) == 2 && segs[1] == "answers":
			id()
			s.putWorkflowAnswers(w, r)
		default:
			notFound(w, r)
		}
	case http.MethodPost:
		switch {
		case len(segs) == 2 && segs[1] == "publish":
			id()
			s.publishWorkflow(w, r)
		case len(segs) == 2 && segs[1] == "duplicate":
			id()
			s.duplicateWorkflow(w, r)
		case len(segs) == 4 && segs[1] == "revisions" && segs[3] == "restore":
			id()
			r.SetPathValue("rev", segs[2])
			s.restoreWorkflow(w, r)
		default:
			notFound(w, r)
		}
	case http.MethodDelete:
		if len(segs) == 1 {
			id()
			s.deleteWorkflow(w, r)
			return
		}
		notFound(w, r)
	default:
		notFound(w, r)
	}
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	items, err := rs.Workflows.List(ctx)
	if err != nil {
		internalError(w)
		return
	}
	uses, err := s.workflowUses(ctx, rs)
	if err != nil {
		internalError(w)
		return
	}
	out := []workflowRowJSON{}
	for _, it := range items {
		out = append(out, workflowRowOf(it, uses[it.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// workflowRowOf adds the check and the libraries to one workflow. The check is the editor's:
// problemCount and the "problems" state come from it, so the list never disagrees with the page.
func workflowRowOf(it flow.ListItem, uses []workflowUseJSON) workflowRowJSON {
	if uses == nil {
		uses = []workflowUseJSON{}
	}
	rep := flow.Validate(it.Flow, flow.ValidateContext{Env: flow.Env{}})
	state := "published"
	if len(rep.Problems) > 0 {
		state = "problems"
	} else if it.DraftAhead {
		state = "draft"
	}
	return workflowRowJSON{
		ID:           strconv.FormatInt(it.ID, 10),
		Name:         it.Name,
		Description:  it.Description,
		Kind:         it.Kind,
		UsedBy:       uses,
		UpdatedAt:    utcJSON(it.UpdatedAt),
		State:        state,
		ProblemCount: len(rep.Problems),
		MadeWith:     madeWithJSON(it.MadeWith),
		// The store keeps the answers only when the guide can say the flow exactly (S2-4).
		FitsGuided: it.Answers != nil,
	}
}

// madeWithJSON maps the row's made_with onto types.ts's "guided" | "editor". A workflow setup made
// from a starter is a guided flow saved unchanged, so it counts as guided.
func madeWithJSON(madeWith string) string {
	if madeWith == "editor" {
		return "editor"
	}
	return "guided"
}

// workflowUses is the libraries' answer to "who uses what", by workflow id.
func (s *Server) workflowUses(ctx context.Context, rs *Deps) (map[int64][]workflowUseJSON, error) {
	libs, err := rs.Catalog.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	uses := map[int64][]workflowUseJSON{}
	for _, lib := range libs {
		if lib.WorkflowID == nil {
			continue
		}
		uses[*lib.WorkflowID] = append(uses[*lib.WorkflowID], workflowUseJSON{
			Type: "library", ID: strconv.FormatInt(lib.ID, 10), Name: lib.Name,
		})
	}
	return uses, nil
}

// createWorkflowBody is POST /workflows: the editor's flow, or the guided builder's answers. A
// flow wins when both are sent: it is the more specific thing the page drew.
type createWorkflowBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Answers     json.RawMessage `json:"answers"`
	Flow        *flow.Flow      `json:"flow"`
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body createWorkflowBody
	if !decode(w, r, &body) {
		return
	}
	madeWith := "editor"
	var f flow.Flow
	switch {
	case body.Flow != nil:
		f = *body.Flow
		if err := flow.CheckShape(f); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_flow", err.Error(), "", 0)
			return
		}
	case answersGiven(body.Answers):
		answers, ok := decodeAnswers(w, body.Answers)
		if !ok {
			return
		}
		made, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
		if len(problems) > 0 {
			writeAnswerProblem(w, problems[0])
			return
		}
		f, madeWith = made, "guided"
	default:
		writeError(w, http.StatusBadRequest, "invalid_flow", "A workflow needs its three steps.", "", 0)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSpace(f.Name)
	}
	if name == "" {
		name = "New workflow"
	}
	f.Name = name
	f.Description = body.Description
	id, rev, err := rs.Workflows.Create(r.Context(), f, madeWith)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID  string `json:"id"`
		Rev int    `json:"rev"`
	}{ID: strconv.FormatInt(id, 10), Rev: rev})
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	info, err := rs.Workflows.Draft(r.Context(), id)
	if errors.Is(err, flow.ErrNotFound) {
		writeWorkflowNotFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	// The three lines (page 09) describe what the library runs: the published revision, not the draft. No published
	// revision, or one whose settings do not read, gives empty lines. The trash days are read now.
	sentences := steps.Sentences{}
	if wf, err := rs.Workflows.Get(r.Context(), id); err == nil && wf.CurrentRev > 0 {
		if rev, err := rs.Workflows.Revision(r.Context(), id, wf.CurrentRev); err == nil {
			// A failed read is -1: the replace line is then empty, not a guessed number of days.
			days, read := s.keepDaysRead(rs)
			if !read {
				days = -1
			}
			if words, err := steps.WorkflowSentences(rev.Flow, days); err == nil {
				sentences = words
			}
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Flow       flow.Flow       `json:"flow"`
		Rev        int             `json:"rev"`
		FitsGuided bool            `json:"fitsGuided"`
		Summary    steps.Sentences `json:"summary"`
	}{Flow: info.Flow, Rev: info.Rev, FitsGuided: info.Answers != nil, Summary: sentences})
}

// answersGiven is true when the body carries an answers object (not absent, not null).
func answersGiven(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// decodeAnswers reads the answers object. The mapping reads the values' types itself; a body that
// is not an object at all is refused here.
func decodeAnswers(w http.ResponseWriter, raw json.RawMessage) (flow.Answers, bool) {
	var answers flow.Answers
	if err := json.Unmarshal(raw, &answers); err != nil || answers == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The answers must be an object.", "answers", 0)
		return nil, false
	}
	return answers, true
}

// writeAnswerProblem is page 06's refusal: the first answer the guide does not offer, on its field.
func writeAnswerProblem(w http.ResponseWriter, p flow.Problem) {
	writeError(w, http.StatusBadRequest, "invalid_answer", p.Message, p.Field, 0)
}

// guideAnswersJSON is getGuideAnswers' answer: the saved answers, whether the guide still fits,
// and the draft revision the page sends back as baseRev when it saves.
type guideAnswersJSON struct {
	Answers    flow.Answers `json:"answers"`
	FitsGuided bool         `json:"fitsGuided"`
	Rev        int          `json:"rev"`
}

// getGuideAnswers is GET /workflows/{id}/answers: the answers the guided page opens with. "new" is
// the guide's own defaults (a workflow the page has not saved yet; rev 0). A saved workflow the
// guide cannot say opens on the defaults with fitsGuided false, so the person starts from somewhere
// usable; saving the answers is then their choice, and the save writes the flow they make.
func (s *Server) getGuideAnswers(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") == "new" {
		writeJSON(w, http.StatusOK, guideAnswersJSON{Answers: flow.DefaultAnswers(), FitsGuided: true})
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	info, err := s.ready.Load().Workflows.Draft(r.Context(), id)
	if errors.Is(err, flow.ErrNotFound) {
		writeWorkflowNotFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	answers := info.Answers
	if answers == nil {
		answers = flow.DefaultAnswers()
	}
	writeJSON(w, http.StatusOK, guideAnswersJSON{Answers: answers, FitsGuided: info.Answers != nil, Rev: info.Rev})
}

// putAnswersBody is PUT /workflows/{id}/answers: the answers the guided page saved, and the draft
// revision it read (baseRev). baseRev is optional until the page sends it; without it the fits
// guard below still refuses to overwrite an editor draft.
type putAnswersBody struct {
	Answers json.RawMessage `json:"answers"`
	BaseRev *int            `json:"baseRev"`
}

// putWorkflowAnswers saves the guided page's answers as the workflow's draft. The answers make the
// flow here (the mapping is the flow package's), and the store keeps the answers only when they
// still say that flow exactly. A guided save never overwrites an editor save silently: a stale
// baseRev is refused; without one the save is allowed (the page that has not sent it yet, and a
// person choosing the guide's flow for a workflow the guide could not read back).
func (s *Server) putWorkflowAnswers(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	var body putAnswersBody
	if !decode(w, r, &body) {
		return
	}
	if !answersGiven(body.Answers) {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is missing answers.", "answers", 0)
		return
	}
	answers, ok := decodeAnswers(w, body.Answers)
	if !ok {
		return
	}
	f, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
	if len(problems) > 0 {
		writeAnswerProblem(w, problems[0])
		return
	}
	// The store makes the lost-update check in the same transaction: an editor save since the
	// guide opened moved the revision on.
	rev, err := rs.Workflows.SaveGuidedDraft(r.Context(), id, f, body.BaseRev)
	var changed *flow.ChangedSinceError
	switch {
	case errors.As(err, &changed):
		writeChangedSince(w, changed.Rev)
	case errors.Is(err, flow.ErrNotFound):
		writeWorkflowNotFound(w)
	case err != nil:
		internalError(w)
	default:
		info, err := rs.Workflows.Draft(r.Context(), id)
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Flow flow.Flow `json:"flow"`
			Rev  int       `json:"rev"`
		}{Flow: info.Flow, Rev: rev})
	}
}

// previewBody is POST /workflows/preview: the answers the page is drawing.
type previewBody struct {
	Answers json.RawMessage `json:"answers"`
}

// previewWorkflow is the guide's "What this does": the sentences, the flow they make and the same
// check the editor runs, nothing saved. An answer the guide does not offer is page 06's refusal.
func (s *Server) previewWorkflow(w http.ResponseWriter, r *http.Request) {
	var body previewBody
	if !decode(w, r, &body) {
		return
	}
	if !answersGiven(body.Answers) {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is missing answers.", "answers", 0)
		return
	}
	answers, ok := decodeAnswers(w, body.Answers)
	if !ok {
		return
	}
	f, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
	if len(problems) > 0 {
		writeAnswerProblem(w, problems[0])
		return
	}
	rep := flow.Validate(f, flow.ValidateContext{Env: s.checkEnv()})
	writeJSON(w, http.StatusOK, struct {
		Sentences []string        `json:"sentences"`
		Flow      flow.Flow       `json:"flow"`
		Problems  []flow.Problem  `json:"problems"`
		Disabled  []flow.Disabled `json:"disabled"`
	}{Sentences: flow.Sentences(f), Flow: f, Problems: rep.Problems, Disabled: rep.Disabled})
}

// tryBody is POST /workflows/try: the flow to try (or the guide's answers) and the file.
type tryBody struct {
	Answers json.RawMessage `json:"answers"`
	Flow    *flow.Flow      `json:"flow"`
	File    string          `json:"file"`
}

// tryWorkflow is S2-5's "Try on one file": one bounded request that answers the lines the pages
// show, and keeps nothing. The flow is the page's own (answers or flow), so it may be unsaved; a
// flow the check refuses is refused here too, before anything is read.
func (s *Server) tryWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body tryBody
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.File) == "" {
		writeError(w, http.StatusBadRequest, "file_required", "Pick a file to try.", "file", 0)
		return
	}
	if !filepath.IsAbs(body.File) {
		writeError(w, http.StatusBadRequest, "invalid_file", "Use the file's full path.", "file", 0)
		return
	}
	var f flow.Flow
	switch {
	case body.Flow != nil && answersGiven(body.Answers):
		writeError(w, http.StatusBadRequest, "invalid_request", "Send the flow or the answers, not both.", "flow", 0)
		return
	case body.Flow != nil:
		if err := flow.CheckShape(*body.Flow); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_flow", err.Error(), "", 0)
			return
		}
		f = *body.Flow
	case answersGiven(body.Answers):
		answers, ok := decodeAnswers(w, body.Answers)
		if !ok {
			return
		}
		var problems []flow.Problem
		f, problems = flow.FromAnswers(answers, flow.DefaultAnswers())
		if len(problems) > 0 {
			writeAnswerProblem(w, problems[0])
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is missing a flow.", "flow", 0)
		return
	}
	// "My languages" is looked up as a run looks it up, so the try shows what the run will keep (review item 4).
	f = flow.ResolveMyLanguages(f, s.myLanguages(r))
	if rep := flow.Validate(f, flow.ValidateContext{Env: s.checkEnv()}); len(rep.Problems) > 0 {
		writeError(w, http.StatusConflict, "has_problems", "Fix this first: "+rep.Problems[0].Message, "", 0)
		return
	}
	// A try reads only what a library owns: the resolved path must sit inside a library's folder.
	inside, err := rs.Catalog.FileInLibrary(r.Context(), body.File)
	if err != nil {
		internalError(w)
		return
	}
	if !inside {
		writeError(w, http.StatusBadRequest, "not_in_library", "That file isn't in one of your libraries. Try a file from a library.", "file", 0)
		return
	}
	if rs.Try == nil {
		internalError(w)
		return
	}
	res, err := rs.Try(r.Context(), f, body.File)
	// A stopped request (the page's Stop) has nobody left to answer.
	if r.Context().Err() != nil {
		return
	}
	var refuse *flow.TryError
	switch {
	case errors.As(err, &refuse):
		writeError(w, http.StatusBadRequest, refuse.Code, refuse.Message, "", 0)
	case err != nil:
		internalError(w)
	default:
		writeJSON(w, http.StatusOK, struct {
			Lines []flow.TryLine `json:"lines"`
		}{Lines: res.Lines})
	}
}

// putDraftBody is PUT /workflows/{id}/draft: the flow the editor holds and the revision it read.
type putDraftBody struct {
	Flow    *flow.Flow `json:"flow"`
	BaseRev *int       `json:"baseRev"`
}

func (s *Server) putWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	var body putDraftBody
	if !decode(w, r, &body) {
		return
	}
	if body.Flow == nil {
		writeError(w, http.StatusBadRequest, "invalid_flow", "A workflow needs its three steps.", "", 0)
		return
	}
	if body.BaseRev == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is missing baseRev.", "baseRev", 0)
		return
	}
	if err := flow.CheckShape(*body.Flow); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_flow", err.Error(), "", 0)
		return
	}
	rev, err := rs.Workflows.SaveDraft(r.Context(), id, *body.Flow, *body.BaseRev)
	var changed *flow.ChangedSinceError
	switch {
	case errors.As(err, &changed):
		writeChangedSince(w, changed.Rev)
	case errors.Is(err, flow.ErrNotFound):
		writeWorkflowNotFound(w)
	case err != nil:
		internalError(w)
	default:
		writeJSON(w, http.StatusOK, struct {
			Rev int `json:"rev"`
		}{Rev: rev})
	}
}

// writeChangedSince is the 409 changed_since: the envelope plus the draft's current rev, so the
// page can say what happened and reload.
func writeChangedSince(w http.ResponseWriter, rev int) {
	body := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Rev int `json:"rev"`
	}{Rev: rev}
	body.Error.Code = "changed_since"
	body.Error.Message = "This workflow was saved somewhere else since you opened it. Reload to see the newer version."
	writeJSON(w, http.StatusConflict, body)
}

// publishBody is POST /workflows/{id}/publish: the editor sends the flow it is publishing; without
// one the stored draft is published (flow-model.md's route).
type publishBody struct {
	Flow *flow.Flow `json:"flow"`
}

func (s *Server) publishWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	var body publishBody
	if !decodeOptional(w, r, &body) {
		return
	}
	rev, err := rs.Workflows.Publish(r.Context(), id, body.Flow, flow.ValidateContext{Env: s.validateEnv(rs)})
	var blocked *flow.PublishBlockedError
	switch {
	case errors.As(err, &blocked):
		writeError(w, http.StatusConflict, "has_problems", "Fix this first: "+blocked.Problem.Message, blocked.Problem.Field, 0)
	case errors.Is(err, flow.ErrNothingToPublish):
		writeError(w, http.StatusBadRequest, "nothing_to_publish", "There is nothing to publish.", "", 0)
	case errors.Is(err, flow.ErrNotFound):
		writeWorkflowNotFound(w)
	case err != nil:
		internalError(w)
	default:
		writeJSON(w, http.StatusOK, struct {
			Rev       int  `json:"rev"`
			Published bool `json:"published"`
		}{Rev: rev, Published: true})
	}
}

func (s *Server) listWorkflowRevisions(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	revs, err := rs.Workflows.Revisions(r.Context(), id)
	if errors.Is(err, flow.ErrNotFound) {
		writeWorkflowNotFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	type revisionJSON struct {
		Rev       int    `json:"rev"`
		CreatedAt string `json:"createdAt"`
	}
	out := []revisionJSON{}
	for _, rev := range revs {
		out = append(out, revisionJSON{Rev: rev.Rev, CreatedAt: utcJSON(rev.CreatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) restoreWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	rev, ok := pathID(r, "rev")
	if !ok {
		writeError(w, http.StatusNotFound, "revision_not_found", "That revision doesn't exist.", "", 0)
		return
	}
	newRev, err := rs.Workflows.Restore(r.Context(), id, int(rev))
	switch {
	case errors.Is(err, flow.ErrNotFound):
		writeWorkflowNotFound(w)
	case errors.Is(err, flow.ErrRevisionNotFound):
		writeError(w, http.StatusNotFound, "revision_not_found", "That revision doesn't exist.", "", 0)
	case err != nil:
		internalError(w)
	default:
		writeJSON(w, http.StatusOK, struct {
			Rev int `json:"rev"`
		}{Rev: newRev})
	}
}

func (s *Server) duplicateWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	copied, err := rs.Workflows.Duplicate(ctx, id)
	if errors.Is(err, flow.ErrNotFound) {
		writeWorkflowNotFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	info, err := rs.Workflows.Draft(ctx, copied.ID)
	if err != nil {
		internalError(w)
		return
	}
	uses, err := s.workflowUses(ctx, rs)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, workflowRowOf(flow.ListItem{
		Workflow: copied, DraftAhead: true, Flow: info.Flow, Answers: info.Answers,
	}, uses[copied.ID]))
}

// deleteWorkflowJSON is the mock's answer (ui/src/api/mockWorkflows.ts): {ok:true}, or
// {ok:false, code:"in_use", usedBy} so the page can name the libraries in its refusal.
type deleteWorkflowJSON struct {
	OK     bool              `json:"ok"`
	Code   string            `json:"code,omitempty"`
	UsedBy []workflowUseJSON `json:"usedBy,omitempty"`
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	id, ok := pathID(r, "id")
	if !ok {
		writeWorkflowNotFound(w)
		return
	}
	if _, err := rs.Workflows.Get(ctx, id); err != nil {
		if errors.Is(err, flow.ErrNotFound) {
			writeWorkflowNotFound(w)
			return
		}
		internalError(w)
		return
	}
	uses, err := s.workflowUses(ctx, rs)
	if err != nil {
		internalError(w)
		return
	}
	if len(uses[id]) > 0 {
		writeJSON(w, http.StatusOK, deleteWorkflowJSON{OK: false, Code: "in_use", UsedBy: uses[id]})
		return
	}
	if err := rs.Workflows.Delete(ctx, id); err != nil {
		if errors.Is(err, flow.ErrNotFound) {
			writeWorkflowNotFound(w)
			return
		}
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, deleteWorkflowJSON{OK: true})
}

func writeWorkflowNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "workflow_not_found", "That workflow doesn't exist.", "", 0)
}

// pathID reads a positive integer path value.
func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// decodeOptional is decode for a route whose body may be absent (publish without a flow).
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "The request is not valid JSON.", "", 0)
	return false
}

// utcJSON formats a time the way the rest of the API does: RFC 3339 in UTC, milliseconds kept.
func utcJSON(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
