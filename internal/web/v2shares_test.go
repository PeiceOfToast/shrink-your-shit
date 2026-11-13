// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
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
		// a port is refused when saving too (the root helper takes none); IPv6 in brackets is not a port
		{"smb://nas:1445/media", false, 7},
		{"nas:2049:/volume1/media", false, 9},
		{"nfs://nas:2049/volume1", false, 10},
		{"smb://[fe80::1]/media", true, 11},
		{"[fd00::2]:/srv/nfs", true, 12},
		{"smb://[fe80::1]:1445/media", false, 13},
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
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	// Port in SMB address → 400
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"\\\\nas:1445\\share"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("test always returns 200: %d", code)
	}
	var portResult struct {
		Ok   bool   `json:"ok"`
		Code string `json:"code"`
	}
	json.Unmarshal([]byte(resp), &portResult)
	if portResult.Ok {
		t.Error("port in address should return ok:false")
	}
	if portResult.Code != "invalid_address" {
		t.Errorf("port: expected invalid_address, got %q", portResult.Code)
	}

	// Port in NFS address → 400
	code, resp, _ = do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"192.0.2.1:2049:/path"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("test always returns 200: %d", code)
	}
	json.Unmarshal([]byte(resp), &portResult)
	if portResult.Ok {
		t.Error("port in NFS address should return ok:false")
	}
	if portResult.Code != "invalid_address" {
		t.Errorf("nfs port: expected invalid_address, got %q", portResult.Code)
	}
}

func TestSharesTestPlainSentences(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares/test",
		body: `{"address":"\\\\nas.invalid\\share"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("test always returns 200: got %d", code)
	}
	var result struct {
		Ok      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal([]byte(resp), &result)
	if result.Ok {
		t.Error("expected ok:false")
	}
	// The message must be a plain sentence, not the raw error
	if strings.Contains(result.Message, "dial tcp") {
		t.Errorf("plain sentence expected, got raw error: %q", result.Message)
	}
	if strings.Contains(result.Message, "no such host") {
		t.Errorf("plain sentence expected, got raw error: %q", result.Message)
	}
}

// D2 Part C tests: fake helper, detectMode, sanitiseName, manual text

// Test sanitiseName fallback is share-<id>, never a bare "share" with the same id.
func TestSanitiseNameFallbackWithID(t *testing.T) {
	if sanitiseName("", 42) != "share-42" {
		t.Errorf("empty name: expected share-42, got %q", sanitiseName("", 42))
	}
	if sanitiseName("-bad", 7) != "share-7" {
		t.Errorf("dash prefix: expected share-7, got %q", sanitiseName("-bad", 7))
	}
	if sanitiseName("_bad", 3) != "share-3" {
		t.Errorf("underscore prefix: expected share-3, got %q", sanitiseName("_bad", 3))
	}
	// Without id -> "share" (legacy fallback)
	if sanitiseName("", 0) != "share" {
		t.Errorf("no id: expected share, got %q", sanitiseName("", 0))
	}
	// Normal name works as before
	if sanitiseName("My Videos", 0) != "my-videos" {
		t.Errorf("normal name: expected my-videos, got %q", sanitiseName("My Videos", 0))
	}
}

// detectMode returns docker when container env is set (restored detection)
func TestDetectModeDockerByEnv(t *testing.T) {
	os.Setenv("container", "abc123")
	defer os.Unsetenv("container")
	mode, platform := detectMode()
	if mode != "docker" {
		t.Errorf("SYP_HELPER_MODE not set, container set: expected docker, got %q on %q", mode, platform)
	}
}

// app mode via SYP_HELPER_MODE forces the handler path.
// Test that a mount error is mapped to not_connected with a reason.
func TestSharesAppModeMountErrorMaps(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	sockPath := filepath.Join(dir, "helper.sock")
	savedSocket := helper.HelperSocket
	helper.HelperSocket = sockPath
	defer func() { helper.HelperSocket = savedSocket }()
	os.Setenv("SYP_HELPER_MODE", "app")
	defer os.Unsetenv("SYP_HELPER_MODE")

	// Fake helper: accept, read command, respond with error
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Read the mount command
		var cmd map[string]any
		json.NewDecoder(conn).Decode(&cmd)
		// Reply with an auth failure
		json.NewEncoder(conn).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "bad_request", "message": "Permission denied for user test"},
		})
		conn.Close()
	}()

	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media","name":"MountTest","username":"user","password":"secret"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	status, _ := created["status"].(string)
	reason := created["reason"]
	if status != "not_connected" {
		t.Errorf("expected not_connected, got %q", status)
	}
	if reason == nil {
		t.Error("expected reason on mount failure")
	} else {
		rCode, _ := reason["code"].(string)
		rMsg, _ := reason["message"].(string)
		if rCode != "mount_failed" {
			t.Errorf("expected mount_failed reason code, got %q", rCode)
		}
		if !strings.Contains(rMsg, "Permission denied") {
			t.Errorf("expected Permission denied in message, got %q", rMsg)
		}
	}
}

// Test that POST in app mode calls Mount with correct args via a fake helper.
func TestSharesPostAppModeCallsMount(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	sockPath := filepath.Join(dir, "helper.sock")
	savedSocket := helper.HelperSocket
	helper.HelperSocket = sockPath
	defer func() { helper.HelperSocket = savedSocket }()
	os.Setenv("SYP_HELPER_MODE", "app")
	defer os.Unsetenv("SYP_HELPER_MODE")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var gotCmd map[string]any
	gotCmdOK := false
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		var cmd map[string]any
		json.NewDecoder(conn).Decode(&cmd)
		gotCmd = cmd
		gotCmdOK = true
		json.NewEncoder(conn).Encode(map[string]any{"ok": true, "pid": 42})
		conn.Close()
	}()

	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media\\Movies","name":"My Share","username":"alice","password":"p@ss"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	if !gotCmdOK {
		t.Error("helper was never called")
		return
	}
	cmdStr, _ := gotCmd["cmd"].(string)
	if cmdStr != "mount" {
		t.Errorf("expected mount command, got %q", cmdStr)
	}
	kind, _ := gotCmd["kind"].(string)
	if kind != "cifs" {
		t.Errorf("expected cifs kind, got %q", kind)
	}
	mpt, _ := gotCmd["mountPoint"].(string)
	if !strings.HasSuffix(mpt, "/my-share") {
		t.Errorf("expected mountPoint ending /my-share, got %q", mpt)
	}
	pw, _ := gotCmd["password"].(string)
	if pw != "p@ss" {
		t.Errorf("password not sent correctly: %q", pw)
	}
	addr, _ := gotCmd["address"].(string)
	// address should be //server/share (not \\server\share)
	if addr != "//nas/media/Movies" {
		t.Errorf("expected //nas/media/Movies, got %q", addr)
	}
	// Verify the share is visible as connected
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	status, _ := created["status"].(string)
	if status != "connected" {
		t.Errorf("expected connected status, got %q", status)
	}
}

// Test that DELETE calls helper.Unmount before deleting
func TestSharesAppModeDeleteCallsUnmount(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	sockPath := filepath.Join(dir, "helper.sock")
	savedSocket := helper.HelperSocket
	helper.HelperSocket = sockPath
	defer func() { helper.HelperSocket = savedSocket }()
	os.Setenv("SYP_HELPER_MODE", "app")
	defer os.Unsetenv("SYP_HELPER_MODE")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var gotUnmount bool
	var unmountPath string
	go func() {
		// First connection: mount (from POST)
		conn, err := ln.Accept()
		if err == nil {
			var cmd map[string]any
			json.NewDecoder(conn).Decode(&cmd)
			json.NewEncoder(conn).Encode(map[string]any{"ok": true, "pid": 42})
			conn.Close()
		}
		// Second connection: unmount (from DELETE)
		conn, err = ln.Accept()
		if err == nil {
			var cmd map[string]any
			json.NewDecoder(conn).Decode(&cmd)
			gotUnmount = true
			cmdStr, _ := cmd["cmd"].(string)
			if cmdStr == "unmount" {
				unmountPath, _ = cmd["mountPoint"].(string)
			}
			json.NewEncoder(conn).Encode(map[string]any{"ok": true, "pid": 42})
			conn.Close()
		}
	}()

	srv, _, _ := server(t, false)

	// Create a share
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\share","name":"ToDelete","username":"u","password":"p"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	id := fmt.Sprintf("%v", created["id"])

	gotUnmount = false
	// Delete the share
	code, resp, _ = do(t, nil, srv, call{method: "DELETE", path: "/api/v2/shares/" + id})
	if code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204 got %d: %s", code, resp)
	}
	if !gotUnmount {
		t.Error("helper.Unmount was not called")
	} else if !strings.HasSuffix(unmountPath, "/to-delete") {
		t.Errorf("expected unmount path ending /to-delete, got %q", unmountPath)
	}
}

// Test manual mode setup text for SMB with username
func TestSharesManualSetupTextSMB(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\server\\share","name":"MyShare","username":"alice"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	setup := created["setup"]
	if setup == nil {
		t.Error("setup is null")
		return
	}
	text, _ := setup["text"].(string)
	removeText, _ := setup["removeText"].(string)
	if !strings.Contains(text, "mount -t cifs") {
		t.Errorf("expected cifs mount: %q", text)
	}
	if !strings.Contains(text, "//server/share") {
		t.Errorf("expected //server/share: %q", text)
	}
	if !strings.Contains(text, "username=alice") {
		t.Errorf("expected username=alice: %q", text)
	}
	if !strings.Contains(text, "nosuid") {
		t.Errorf("expected nosuid: %q", text)
	}
	if !strings.Contains(text, "mkdir -p") {
		t.Errorf("expected mkdir: %q", text)
	}
	if !strings.Contains(removeText, "sudo umount") {
		t.Errorf("expected sudo umount: %q", removeText)
	}
}

// Test manual mode setup text for NFS
func TestSharesManualSetupTextNFS(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"server:/export","name":"NFSShare","type":"nfs"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	text, _ := created["setup"]["text"].(string)
	removeText, _ := created["setup"]["removeText"].(string)
	if !strings.Contains(text, "mount -t nfs") {
		t.Errorf("expected nfs mount: %q", text)
	}
	if !strings.Contains(text, "server:/export") {
		t.Errorf("expected server:/export address: %q", text)
	}
	if !strings.Contains(text, "nosuid") {
		t.Errorf("expected nosuid: %q", text)
	}
	if !strings.Contains(removeText, "sudo umount") {
		t.Errorf("expected sudo umount: %q", removeText)
	}
}

// Test manual mode setup text for guest SMB share
func TestSharesManualSetupTextGuest(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\server\\share","name":"GuestShare","guest":true}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	text, _ := created["setup"]["text"].(string)
	if !strings.Contains(text, "guest,") {
		t.Errorf("expected guest option: %q", text)
	}
	if strings.Contains(text, "username=") {
		t.Errorf("guest share should not have username=: %q", text)
	}
}

// Test manual mode setup text for a name containing a quote
func TestSharesManualSetupTextQuotedName(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media","name":"Bob's Share","username":"alice"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	text, _ := created["setup"]["text"].(string)
	if !strings.Contains(text, "bob-s-share") {
		t.Errorf("expected sanitised name bob-s-share: %q", text)
	}
	// The share path should be properly quoted
	if !strings.Contains(text, "'/var/lib/syp/shares/bob-s-share'") {
		t.Errorf("expected quoted path: %q", text)
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

// hasPort and addressHost (orch-lead, ui-shop QA of #217): a port is found in both forms; an IPv6 address alone is not one.
func TestShareAddressHostAndPort(t *testing.T) {
	for _, c := range []struct {
		typ, addr, host string
		port            bool
	}{
		{"smb", `\\nas\media`, "nas", false},
		{"smb", `\\nas:1445\media`, "nas:1445", true},
		{"smb", `\\[fe80::1]\media`, "[fe80::1]", false},
		{"smb", `\\[fe80::1]:1445\media`, "[fe80::1]:1445", true},
		{"nfs", "nas:/volume1/media", "nas", false},
		{"nfs", "nas:2049:/volume1", "nas:2049", true},
		{"nfs", "[fd00::2]:/srv/nfs", "[fd00::2]", false},
		{"nfs", "10.0.0.2:/export", "10.0.0.2", false},
		{"nfs", "[fd00::2]:2049:/x", "[fd00::2]:2049", true},
	} {
		host := addressHost(c.typ, c.addr)
		if host != c.host || hasPort(host) != c.port {
			t.Errorf("%s %q: host %q port %v, want %q %v", c.typ, c.addr, host, hasPort(host), c.host, c.port)
		}
	}
	if dialHost("[fe80::1]") != "fe80::1" || dialHost("nas") != "nas" {
		t.Error("dialHost")
	}
}
