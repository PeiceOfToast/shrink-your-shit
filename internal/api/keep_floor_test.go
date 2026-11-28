// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
)

// publishedReplace publishes a workflow whose save replaces the original, through the store the server uses.
func publishedReplace(t *testing.T, e *env) {
	t.Helper()
	a := flow.DefaultAnswers()
	a["original"] = "replace"
	f, problems := flow.FromAnswers(a, flow.DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	fs := flow.Open(e.db)
	id, _, err := fs.Create(context.Background(), f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Publish(context.Background(), id, &f, flow.ValidateContext{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// K17 rule 1: lowering the keep below the floor is refused while a workflow replaces originals; nothing is saved.
func TestSettingsRefusesKeepBelowFloorWhileReplaceIsUsed(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := withSession(cookie)
	publishedReplace(t, e)
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"processing":{"keepOriginalsDays":0}}`, write(h), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body)
	}
	code, message, _ := errorBody(t, rec)
	if code != "invalid_setting" || message != "Originals are kept at least 7 days while a workflow replaces originals." {
		t.Errorf("error = %q %q", code, message)
	}
	rec = e.do(http.MethodGet, "/api/v2/settings", "", h, false)
	if !strings.Contains(rec.Body.String(), `"keepOriginalsDays":7`) {
		t.Errorf("the keep changed by a refused patch: %s", rec.Body)
	}
}

// K17 rule 3: with no replace workflow, 0 days still saves.
func TestSettingsKeepZeroSavesWithNoReplace(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := withSession(cookie)
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"processing":{"keepOriginalsDays":0}}`, write(h), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rec.Code, rec.Body)
	}
}

// settingsPatchForTest lowers the keep directly in the settings store, as a test setup step.
func settingsPatchForTest(e *env, text string) (any, any, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		return nil, nil, err
	}
	_, _, err := settings.Patch(context.Background(), e.db, settings.Options{}, body)
	return nil, nil, err
}

// K17 path 1 at the page: publishing a replace while the keep is below the floor answers 409 with the Save step's
// where field and the plain sentence.
func TestPublishRouteRefusesReplaceBelowFloor(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := withSession(cookie)
	a := flow.DefaultAnswers()
	a["original"] = "replace"
	f, _ := flow.FromAnswers(a, flow.DefaultAnswers())
	id, _, err := flow.Open(e.db).Create(context.Background(), f, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Lower the keep through the settings store (the route itself refuses while no replace is published).
	if _, _, err := settingsPatchForTest(e, `{"processing":{"keepOriginalsDays":0}}`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, fmt.Sprintf("/api/v2/workflows/%d/publish", id), "",
		write(h), false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body %s", rec.Code, rec.Body)
	}
	code, message, _ := errorBody(t, rec)
	if code != "has_problems" || !strings.Contains(message, "Keep originals for at least 7 days") {
		t.Errorf("error = %q %q", code, message)
	}
}

// The page's contract for the refusal: the code, the field and the sentence the Settings page shows (K17).
func TestContractSettingsKeepRefusal(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := withSession(cookie)
	publishedReplace(t, e)
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"processing":{"keepOriginalsDays":0}}`, write(h), false)
	writeGolden(t, "settings-keep-refusal", "ApiError", http.MethodPatch, "/api/v2/settings", rec)
}
