// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// TestSettingsGetReturnsTheDocument: GET /settings answers the page's groups with the defaults.
func TestSettingsGetReturnsTheDocument(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/api/v2/settings", "", map[string]string{"Cookie": cookie.String()}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var doc struct {
		Processing struct {
			KeepOriginalsDays int    `json:"keepOriginalsDays"`
			TrashPath         string `json:"trashPath"`
		} `json:"processing"`
		BackupKeep int `json:"backupKeep"`
	}
	decodeInto(t, rec, &doc)
	if doc.Processing.KeepOriginalsDays != 7 || doc.Processing.TrashPath != ".syp-trash" || doc.BackupKeep != 10 {
		t.Errorf("document = %+v", doc)
	}
}

// TestSettingsPatchRefusalIsA400AndSavesNothing: a refused field answers 400 with its code and field, and the other
// fields in the same patch are not saved.
func TestSettingsPatchRefusalIsA400AndSavesNothing(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := map[string]string{"Cookie": cookie.String()}
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"processing":{"parallelJobs":2,"keepOriginalsDays":400}}`, write(h), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body)
	}
	code, message, _ := errorBody(t, rec)
	var env struct {
		Error struct {
			Field string `json:"field"`
		} `json:"error"`
	}
	decodeInto(t, rec, &env)
	if code != "invalid_setting" || env.Error.Field != "processing.keepOriginalsDays" || message != "Keep originals from 0 to 365 days." {
		t.Errorf("error = %q %q field %q", code, message, env.Error.Field)
	}
	rec = e.do(http.MethodGet, "/api/v2/settings", "", h, false)
	if !strings.Contains(rec.Body.String(), `"parallelJobs":1`) {
		t.Errorf("parallelJobs changed by a refused patch: %s", rec.Body)
	}
}

// TestSettingsPatchWritesOneEventPerChange: each saved change writes one settings.changed event.
func TestSettingsPatchWritesOneEventPerChange(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := map[string]string{"Cookie": cookie.String()}
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"general":{"serverName":"Den"},"processing":{"keepOriginalsDays":30}}`, write(h), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	e.closeBus()
	evs, _, err := e.bus.Query(t.Context(), events.Filter{Source: "settings"}, events.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Code == "settings.changed" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("settings.changed events = %d, want one per change (2)", n)
	}
}

// M1: a null list is refused with its field, and nothing is saved; the page's list stays a list.
func TestSettingsRefusesNullListsAndSavesNothing(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := map[string]string{"Cookie": cookie.String()}
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"general":{"languages":null}}`, write(h), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body)
	}
	var env struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	decodeInto(t, rec, &env)
	if env.Error.Code != "invalid_setting" || env.Error.Field != "general.languages" {
		t.Errorf("error = %+v, want invalid_setting on general.languages", env.Error)
	}
	rec = e.do(http.MethodGet, "/api/v2/settings", "", h, false)
	if strings.Contains(rec.Body.String(), `"languages":null`) {
		t.Errorf("the page's languages became null: %s", rec.Body)
	}
}

// M2: the PATCH body is capped like every other body route: an oversized body is refused, not read whole.
func TestSettingsPatchBodyIsCapped(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	h := map[string]string{"Cookie": cookie.String()}
	big := `{"general":{"serverName":"` + strings.Repeat("x", 70<<10) + `"}}`
	rec := e.do(http.MethodPatch, "/api/v2/settings", big, write(h), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d for a 70 KiB body, want 400; body %s", rec.Code, rec.Body)
	}
}
