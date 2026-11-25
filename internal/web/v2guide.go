// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/integrations"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/notify"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// The guided builder's API (slice 12, G1). The screen asks these for the defaults and what this
// computer can make, previews the answers as sentences, and saves answers into a draft; it never builds
// a workflow document itself. Creating a workflow from answers is POST /api/v2/workflows with "answers".
func v2Guide(api *http.ServeMux, store *jobs.Store, svc *integrations.Service, notifier *notify.Service) {
	// GET: the defaults per goal, the choices this computer can make, what exists for the cards after the
	// save, and the person's languages (the picker's starting list).
	api.HandleFunc("GET /api/v2/workflows/guide", func(w http.ResponseWriter, r *http.Request) {
		set, err := LoadSettings(store)
		if err != nil {
			serverError(w, err)
			return
		}
		after := map[string]any{"mediaServer": false, "downloader": false, "notifyTargets": []map[string]string{}}
		if list, err := svc.List(); err == nil {
			for _, i := range list {
				if !i.Enabled {
					continue
				}
				switch i.Kind {
				case integrations.Plex, integrations.Jellyfin:
					after["mediaServer"] = true
				case integrations.Sonarr, integrations.Radarr:
					after["downloader"] = true
				}
			}
		}
		if ns, err := notifier.Get(); err == nil {
			targets := []map[string]string{}
			for _, t := range ns.Targets {
				if t.Enabled {
					targets = append(targets, map[string]string{"id": t.ID, "name": t.Name})
				}
			}
			after["notifyTargets"] = targets
		}
		writeJSON(w, map[string]any{
			"defaults": map[string]workflow.Answers{workflow.GoalSmaller: workflow.DefaultAnswers(workflow.GoalSmaller),
				workflow.GoalEveryDevice: workflow.DefaultAnswers(workflow.GoalEveryDevice), workflow.GoalBoth: workflow.DefaultAnswers(workflow.GoalBoth)},
			"available": map[string]any{"formats": available(workflow.TypeConvert, "format"), "audio": available(workflow.TypeAudio, "audio"),
				"subtitles": available(workflow.TypeSubtitles, "subtitles"), "after": after},
			"languages": orEmptyStrings(set.Languages),
		})
	})
	// Preview: the answers as the sentences the last page shows, the steps they build, and what validation
	// says. Nothing is saved.
	api.HandleFunc("POST /api/v2/workflows/guide/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Answers workflow.Answers }
		if !readJSON(w, r, &in) {
			return
		}
		a := workflow.Fill(in.Answers)
		if !answersOK(w, a) {
			return
		}
		doc := workflow.Build(a)
		sc, err := workflowScopes(store)
		if err != nil {
			serverError(w, err)
			return
		}
		res := workflow.ValidateWith(doc, sc)
		writeJSON(w, map[string]any{"summary": workflow.Summary(a), "steps": workflow.Steps(doc), "validation": res.Issues})
	})
	// Save answers into a draft: only the steps the questions own change; the rest stays. The draft's
	// version (If-Match) is required, as for a draft save.
	api.HandleFunc("PUT /api/v2/workflows/{id}/answers", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
			return
		}
		var in struct{ Answers workflow.Answers }
		if !readJSON(w, r, &in) {
			return
		}
		if r.Header.Get("If-Match") == "" {
			apiError(w, http.StatusPreconditionRequired, "if_match_required", "Reload this workflow and try again.")
			return
		}
		if _, err := store.EnsureRevision(id); err != nil {
			guideStoreErr(w, err)
			return
		}
		cur, _, err := store.Draft(id)
		if err != nil {
			guideStoreErr(w, err)
			return
		}
		if g := workflow.GuideOf(cur.Doc); g.Coverage == workflow.CoverageNone {
			writeErrorWith(w, http.StatusConflict, "not_guided", g.Why+" Change it in the steps, or start over from the questions as a new workflow. Nothing was changed.", map[string]any{"why": g.Why})
			return
		}
		a := workflow.Fill(in.Answers)
		if !answersOK(w, a) {
			return
		}
		d, err := store.SaveDraft(id, workflow.Apply(cur.Doc, a), cur.Layout, r.Header.Get("If-Match"))
		if err != nil {
			guideStoreErr(w, err)
			return
		}
		if in.Answers.LanguagesEverywhere {
			if err := saveLanguages(store, in.Answers.Languages); err != nil {
				serverError(w, err)
				return
			}
		}
		sc, err := workflowScopes(store)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, revisionView(d, sc))
	})
}

// answersOK refuses an answer outside its choices, naming the question (field) so the screen shows the
// message there.
func answersOK(w http.ResponseWriter, a workflow.Answers) bool {
	iss := workflow.Check(a)
	if len(iss) == 0 {
		return true
	}
	writeErrorWith(w, http.StatusBadRequest, iss[0].Code, iss[0].Message+" Nothing was changed.", map[string]any{"field": iss[0].Field, "issues": iss})
	return false
}

func guideStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
	case errors.Is(err, jobs.ErrStaleDraft):
		apiError(w, http.StatusConflict, "stale_draft", "This workflow was changed elsewhere since you opened it. Reload it to see the changes. Nothing was changed.")
	default:
		serverError(w, err)
	}
}

// saveLanguages makes codes the person's languages (Settings > General): "also use these everywhere".
func saveLanguages(store *jobs.Store, codes []string) error {
	ss := &settingsStore{store: store}
	s, err := ss.load()
	if err != nil {
		return err
	}
	s.Languages = canonLanguages(codes)
	return ss.save(s)
}

// available are the choices of a step's setting this computer can make (the schema's x-available), or
// every choice when the schema doesn't say.
func available(typ, key string) []string {
	def, ok := workflow.Definition(typ)
	if !ok {
		return []string{}
	}
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	field, _ := props[key].(map[string]any)
	for _, k := range []string{"x-available", "enum"} {
		switch v := field[k].(type) {
		case []string:
			return v
		case []any:
			out := []string{}
			for _, x := range v {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return []string{}
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
