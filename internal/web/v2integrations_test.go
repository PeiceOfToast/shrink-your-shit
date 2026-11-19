// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/integrations"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

const webTestKey = "web-secret-key-987"

// The Sonarr and Radarr routes: the contract's error codes, a key that is never returned, and 204 on delete.
func TestV2IntegrationsRoutes(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := http.NewServeMux()
	v2Integrations(api, integrations.New(st, integrations.Options{}))

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}
	codeOf := func(rec *httptest.ResponseRecorder) string {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(rec.Body.Bytes(), &e)
		return e.Error.Code
	}

	bad := map[string]struct {
		body string
		code string
	}{
		"invalid_url":     {`{"kind":"sonarr","name":"a","url":"ftp://x","apiKey":"k"}`, "invalid_url"},
		"invalid_kind":    {`{"kind":"emby","name":"a","url":"http://x","apiKey":"k"}`, "invalid_kind"},
		"missing_key":     {`{"kind":"sonarr","name":"a","url":"http://x"}`, "missing_key"},
		"invalid_mapping": {`{"kind":"sonarr","name":"a","url":"http://x","apiKey":"k","pathMappings":[{"theirPath":"tv","ourPath":"/m"}]}`, "invalid_mapping"},
	}
	for name, c := range bad {
		rec := do("POST", "/api/v2/integrations", c.body)
		if rec.Code != http.StatusBadRequest || codeOf(rec) != c.code {
			t.Errorf("%s: status %d code %q, want 400 %s (%s)", name, rec.Code, codeOf(rec), c.code, rec.Body.String())
		}
	}

	rec := do("POST", "/api/v2/integrations", `{"kind":"sonarr","name":"Home","url":"http://127.0.0.1:1","apiKey":"`+webTestKey+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	list := do("GET", "/api/v2/integrations", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), webTestKey) {
		t.Fatalf("list: %d, key present=%v", list.Code, strings.Contains(list.Body.String(), webTestKey))
	}
	if !strings.Contains(list.Body.String(), `"hasKey":true`) {
		t.Fatalf("list lacks hasKey: %s", list.Body.String())
	}

	if rec := do("PATCH", "/api/v2/integrations/"+created.ID, `{"name":"Renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch without key: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("PATCH", "/api/v2/integrations/nope", `{"name":"x"}`); rec.Code != http.StatusNotFound || codeOf(rec) != "not_found" {
		t.Fatalf("patch unknown: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("DELETE", "/api/v2/integrations/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do("DELETE", "/api/v2/integrations/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := do("POST", "/api/v2/integrations/test", `{"kind":"sonarr","url":"http://127.0.0.1:1","apiKey":"`+webTestKey+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("test form: %d %s", rec.Code, rec.Body.String())
	} else if strings.Contains(rec.Body.String(), webTestKey) {
		t.Fatal("the key came back in a test response")
	}
}
