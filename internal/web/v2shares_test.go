// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
		id      int // unique id for the name
	}{
		{"smb://nas/media", true, 1},
		{"nas:/volume1/media", true, 2},
		{"nfs://server:/export", true, 3},
		{"", false, 4},
		{"nas", false, 5},
		{"smb://", false, 6},
	}
	for _, c := range checks {
		name := "Test" + strconv.Itoa(c.id)
		ql := `{"address":"` + c.address + `","name":"` + name + `"}`
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
	smbPort = closedPort(t) // this machine may run its own SMB server on 445 (Windows does)
	defer func() { smbPort = "445" }()

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

// #217 fix 1: NFS address keeps forward slashes in the response.
func TestSharesNFSAddressKeepsSlashes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body: `{"address":"nas:/volume1/media","name":"NFS Test"}`, ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var share struct{ Address string }
	json.Unmarshal([]byte(resp), &share)
	if strings.Contains(share.Address, "\\") {
		t.Errorf("NFS address has backslashes: %q", share.Address)
	}
}

// #217 fix 2: PATCH only changes the fields sent.
func TestSharesPATCHOnlyChangesSentFields(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\share","name":"Original","username":"DOMAIN\\user","guest":true}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	id := fmt.Sprintf("%v", created["id"])
	// PATCH with only name should keep username and guest untouched
	code, resp, _ = do(t, nil, srv, call{method: "PATCH", path: "/api/v2/shares/" + id,
		body: `{"name":"Renamed"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, resp)
	}
	var patched map[string]any
	json.Unmarshal([]byte(resp), &patched)
	if patched["username"] != "DOMAIN\\user" {
		t.Errorf("username changed: %v", patched["username"])
	}
	if patched["guest"] != true {
		t.Errorf("guest changed: %v", patched["guest"])
	}
}

// #217 fix 3: duplicate name returns 409 name_taken.
func TestSharesDuplicateNameIs409(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	body := `{"address":"\\\\nas\\share","name":"My NAS"}`
	code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares", body: body, ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("first POST: %d", code)
	}
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares", body: body, ctype: "application/json"})
	if code != http.StatusConflict {
		t.Fatalf("duplicate: expected 409 got %d: %s", code, resp)
	}
	var errResp struct{ Error *struct{ Code string } }
	json.Unmarshal([]byte(resp), &errResp)
	if errResp.Error == nil || errResp.Error.Code != "name_taken" {
		t.Errorf("expected name_taken, got %v", errResp)
	}
}

// #217 fix 4: unknown type is 400 invalid.
func TestSharesUnknownTypeIs400(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body: `{"type":"webdav","address":"\\\\nas\\share","name":"Bad"}`, ctype: "application/json"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad type: expected 400 got %d: %s", code, resp)
	}
	code, resp, _ = do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"type":"ftp","address":"10.0.0.1:/path"}`, ctype: "application/json"})
	var result struct{ Ok bool }
	json.Unmarshal([]byte(resp), &result)
	if result.Ok {
		t.Error("unknown type should return ok:false")
	}
}

// #217 fix 5: empty list returns [], not null.
func TestSharesEmptyListIsArray(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	_, resp, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/shares"})
	var list struct{ Items any }
	json.Unmarshal([]byte(resp), &list)
	if list.Items == nil {
		t.Error("items is null, should be []")
	}
}

// #217 fix 6: unknown host returns host_not_found, not mount_failed.
// Uses the reserved .invalid TLD (RFC 6761) so the DNS lookup fails fast and
// does not depend on the network or the local resolver's timeout.
func TestSharesTestUnknownHostIsHostNotFound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	start := time.Now()
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"\\\\nas.invalid\\share"}`, ctype: "application/json"})
	elapsed := time.Since(start)
	if code != http.StatusOK {
		t.Fatalf("test: %d %s", code, resp)
	}
	var result struct {
		Ok   bool   `json:"ok"`
		Code string `json:"code"`
	}
	json.Unmarshal([]byte(resp), &result)
	if result.Ok {
		t.Error("expected ok to be false")
	}
	if elapsed > 3*time.Second {
		t.Errorf("test took %v, want under ~3s", elapsed)
	}
	if result.Code != "host_not_found" && result.Code != "host_unreachable" && result.Code != "timeout" {
		t.Errorf("expected host_not_found/host_unreachable/timeout, got %q", result.Code)
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
		{"logon failure: unknown user name or bad password", "auth_failed"},
		{"access denied", "access_denied"},
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

// closedPort is a local TCP port with nothing listening: one that was free a moment ago.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	return port
}
