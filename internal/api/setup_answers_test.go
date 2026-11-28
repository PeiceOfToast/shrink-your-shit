// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// setupAnswersBody is page 13's call with the starter's answers as the setup page sends them: a
// starter id and the chips' answers. answers nil leaves the answers out of the body altogether.
func setupAnswersBody(t *testing.T, answers map[string]any) string {
	t.Helper()
	starter := map[string]any{"id": "shrink-videos"}
	if answers != nil {
		starter["answers"] = answers
	}
	return mustJSON(t, map[string]any{
		"account": map[string]any{"password": testPassword},
		"library": nil,
		"starter": starter,
	})
}

// starterOwnAnswers is what page 05's card opens the chips on: the starter's own answers.
func starterOwnAnswers(t *testing.T) map[string]any {
	t.Helper()
	st, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	own, fits := flow.ToAnswers(st.Flow)
	if !fits {
		t.Fatal("the starter's flow does not fit the guide")
	}
	out := make(map[string]any, len(own))
	for k, v := range own {
		out[k] = v
	}
	return out
}

// TestSetupUsesTheAnswers is page 13's Finish with the chips answered: the published revision's
// flow carries the person's choices, not the starter's defaults.
func TestSetupUsesTheAnswers(t *testing.T) {
	e := newEnv(t, true)
	answers := starterOwnAnswers(t)
	answers["quality"] = 5
	answers["format"] = "h264"

	rec := e.do(http.MethodPost, "/api/v2/setup", setupAnswersBody(t, answers), write(nil), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s; want 200", rec.Code, rec.Body)
	}

	rev, err := e.flows.Revision(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("read revision 1: %v", err)
	}
	if got := apiQualityOf(t, rev.Flow); got != 5 {
		t.Errorf("the setup workflow's quality = %d; want the answer's 5", got)
	}
	var set steps.VideoSettings
	if err := json.Unmarshal(rev.Flow.Steps[0].Settings, &set); err != nil {
		t.Fatal(err)
	}
	if set.Format != "h264" {
		t.Errorf("the setup workflow's format = %q; want the answer's h264", set.Format)
	}
	if rev.Flow.Name != "Shrink videos" {
		t.Errorf("the setup workflow's name = %q; want the starter's name", rev.Flow.Name)
	}

	// The saved answers are the person's, so the guided page opens on them rather than the
	// starter's defaults.
	cookie := cookieNamed(t, rec, sessionCookie)
	var saved guideAnswers
	get(t, e, cookie, "/api/v2/workflows/1/answers", &saved)
	if !saved.FitsGuided {
		t.Fatalf("the setup workflow does not fit the guide: %+v", saved)
	}
	if got, _ := saved.Answers["quality"].(float64); int(got) != 5 {
		t.Errorf("the saved answers' quality = %v; want 5", saved.Answers["quality"])
	}
	if got, _ := saved.Answers["format"].(string); got != "h264" {
		t.Errorf("the saved answers' format = %v; want h264", saved.Answers["format"])
	}
}

// TestSetupRefusesABadAnswerWithoutSaving is the all-or-nothing half of page 13: an answer the
// guide does not offer is 400 invalid_answer on its field, and no password, library or workflow is
// saved.
func TestSetupRefusesABadAnswerWithoutSaving(t *testing.T) {
	e := newEnv(t, true)
	answers := starterOwnAnswers(t)
	answers["format"] = "nope"

	rec := e.do(http.MethodPost, "/api/v2/setup", setupAnswersBody(t, answers), write(nil), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("setup = %d %s; want 400", rec.Code, rec.Body)
	}
	code, _, raw := errorBody(t, rec)
	if code != "invalid_answer" {
		t.Errorf("code = %q; want invalid_answer", code)
	}
	if field, _ := raw["field"].(string); field != "starter.answers.format" {
		t.Errorf("field = %q; want starter.answers.format", field)
	}
	if len(rec.Result().Cookies()) > 0 {
		t.Error("a refused setup signed the browser in")
	}

	ctx := context.Background()
	items, err := e.flows.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("workflows = %d; a refused setup saved one", len(items))
	}
	libs, err := e.catalog.ListLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 0 {
		t.Errorf("libraries = %d; a refused setup saved one", len(libs))
	}
	// The account is still unset: the same call without the bad answer goes through.
	rec = e.do(http.MethodPost, "/api/v2/setup", setupNoLibrary, write(nil), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("a clean setup after the refusal = %d %s; want 200", rec.Code, rec.Body)
	}
}

// TestSetupRefusesAGreyedAnswer: a choice the guide greys today (a different folder) is refused
// the way the guide refuses it, on the answer's own field.
func TestSetupRefusesAGreyedAnswer(t *testing.T) {
	e := newEnv(t, true)
	answers := starterOwnAnswers(t)
	answers["original"] = "one_folder"

	rec := e.do(http.MethodPost, "/api/v2/setup", setupAnswersBody(t, answers), write(nil), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("setup = %d %s; want 400", rec.Code, rec.Body)
	}
	code, message, raw := errorBody(t, rec)
	if code != "invalid_answer" {
		t.Errorf("code = %q; want invalid_answer", code)
	}
	if field, _ := raw["field"].(string); field != "starter.answers.original" {
		t.Errorf("field = %q; want starter.answers.original", field)
	}
	if message != flow.FolderWhy {
		t.Errorf("message = %q; want the guide's reason %q", message, flow.FolderWhy)
	}
}

// TestSetupWithoutAnswersMakesTheStarter keeps today's behaviour: no answers means the starter's
// own flow, published as revision 1.
func TestSetupWithoutAnswersMakesTheStarter(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", setupAnswersBody(t, nil), write(nil), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s; want 200", rec.Code, rec.Body)
	}
	st, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	rev, err := e.flows.Revision(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("read revision 1: %v", err)
	}
	if apiQualityOf(t, rev.Flow) != apiQualityOf(t, st.Flow) {
		t.Errorf("the setup workflow's quality = %d; want the starter's %d",
			apiQualityOf(t, rev.Flow), apiQualityOf(t, st.Flow))
	}
	if rev.Flow.Name != st.Name || rev.Flow.Description != st.Description {
		t.Errorf("the setup workflow = %q/%q; want the starter's %q/%q",
			rev.Flow.Name, rev.Flow.Description, st.Name, st.Description)
	}
}
