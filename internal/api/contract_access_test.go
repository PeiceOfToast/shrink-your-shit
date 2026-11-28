// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The access goldens (page 11). The token is random, so each golden swaps it for $DIR: the body keeps its shape.
func TestContractAccessGoldens(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/access/password", `{"password":"`+newPassword+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password = %d", rec.Code)
	}
	writeGolden(t, "access-password", "none", http.MethodPost, "/api/v2/access/password", rec)

	before := e.do(http.MethodGet, "/api/v2/access/token", "", withSession(cookie), false)
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &got); err != nil || got.Token == "" {
		t.Fatalf("token body = %s: %v", before.Body, err)
	}
	writeGolden(t, "access-token", "AccessToken", http.MethodGet, "/api/v2/access/token", before, got.Token)

	rotated := e.do(http.MethodPost, "/api/v2/access/token/rotate", "", write(withSession(cookie)), false)
	var fresh struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &fresh); err != nil || fresh.Token == "" {
		t.Fatalf("rotate body = %s: %v", rotated.Body, err)
	}
	writeGolden(t, "access-token-rotate", "AccessToken", http.MethodPost, "/api/v2/access/token/rotate", rotated, fresh.Token)
}
