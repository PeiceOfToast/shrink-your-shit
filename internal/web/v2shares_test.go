// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharesCreateAndList(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	// POST a share — use a raw JSON string with proper escaping
	const body = `{"address":"\\\\nas\\media","name":"NAS Videos","username":"user","password":"secret"}`
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares", body: body, ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	if strings.Contains(strings.ToLower(resp), "secret") {
		t.Errorf("password leaked in POST response: %q", resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	if created["id"] == nil {
		t.Fatalf("no id in POST response: %q", resp)
	}
	// List shares
	code, resp, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/shares"})
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, resp)
	}
	if strings.Contains(strings.ToLower(resp), "secret") {
		t.Errorf("password leaked in GET response: %q", resp)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal([]byte(resp), &list)
	if len(list.Items) == 0 {
		t.Error("list has no items")
	}
}

func TestSharesPATCHKeepsPassword(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	const body = `{"address":"\\\\nas\\media","name":"NAS Videos","username":"user","password":"secret"}`
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares", body: body, ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	if created["hasPassword"] != true {
		t.Error("hasPassword is false after save")
	}
	id := fmt.Sprintf("%v", created["id"])
	// PATCH with empty password keeps hasPassword true
	code, resp, _ = do(t, nil, srv, call{method: "PATCH", path: "/api/v2/shares/" + id,
		body: `{"name":"Updated"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, resp)
	}
	var patched map[string]any
	json.Unmarshal([]byte(resp), &patched)
	if patched["hasPassword"] != true {
		t.Error("hasPassword became false after PATCH with empty password")
	}
}

func TestSharesAddressParsing(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	var checks = []struct {
		address string
		valid   bool
	}{
		{"smb://nas/media", true},
		{"nas:/volume1/media", true},
		{"nfs://server:/export", true},
		{"", false},
		{"nas", false},
		{"smb://", false},
	}
	for _, c := range checks {
		ql := `{"address":"` + c.address + `","name":"Test"}`
		code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares", body: ql, ctype: "application/json"})
		if c.valid && code != http.StatusCreated {
			t.Errorf("%q: expected 201 got %d: %s", c.address, code, resp)
		}
		if !c.valid && code != http.StatusBadRequest {
			t.Errorf("%q: expected 400 got %d: %s", c.address, code, resp)
		}
	}
}

func TestSharesListHasModeFields(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	_, resp, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/shares"})
	var list struct {
		Mode, Platform string
		NfsSupported   bool `json:"nfsSupported"`
		TestedPlatform bool `json:"testedPlatform"`
	}
	json.Unmarshal([]byte(resp), &list)
	if list.Mode == "" {
		t.Error("mode is empty")
	}
	if list.Platform == "" {
		t.Error("platform is empty")
	}
}

func TestSharesTestInvalidAddress(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"bad"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("test always returns 200: got %d", code)
	}
	var result struct {
		Ok   bool   `json:"ok"`
		Code string `json:"code"`
	}
	json.Unmarshal([]byte(resp), &result)
	if result.Ok {
		t.Error("invalid address should return ok:false")
	}
	if result.Code != "invalid_address" {
		t.Errorf("expected code invalid_address, got %q", result.Code)
	}
}

func TestSharesTestUnreachable(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"\\\\127.0.0.1\\share"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("test always returns 200: got %d", code)
	}
	var result struct {
		Ok   bool   `json:"ok"`
		Code string `json:"code"`
	}
	json.Unmarshal([]byte(resp), &result)
	if result.Ok {
		t.Log("SMB server answered on 127.0.0.1:445 — test requires env or real SMB")
		return
	}
	if result.Code != "host_unreachable" && result.Code != "timeout" {
		t.Errorf("expected host_unreachable or timeout, got %q", result.Code)
	}
}

func TestSharesTestSMBCodes(t *testing.T) {
	tests := []struct {
		err  string
		code string
	}{
		{"connection refused", "host_unreachable"},
		{"no route to host", "host_unreachable"},
		{"i/o timeout", "timeout"},
		{"connection timed out", "timeout"},
		{"authentication failed", "auth_failed"},
		{"login failed", "auth_failed"},
		{"access denied", "auth_failed"},
		{"network path not found", "share_not_found"},
		{"the specified share does not exist", "share_not_found"},
		{"something else", "mount_failed"},
	}
	for _, c := range tests {
		result := smbError(errors.New(c.err))
		if got := result["code"].(string); got != c.code {
			t.Errorf("%q: code %q, want %q", c.err, got, c.code)
		}
	}
}
