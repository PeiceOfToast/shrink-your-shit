// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		t.Errorf("container set: expected docker, got %q on %q", mode, platform)
	}
}

// fakeHelper answers each helper call on sock with reply(cmd) and records the
// commands. detectMode's probe connects and closes without sending, so it is
// skipped rather than served.
func fakeHelper(t *testing.T, sock string, reply func(map[string]any) map[string]any) func() []map[string]any {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var cmds []map[string]any
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var cmd map[string]any
			if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
				conn.Close()
				continue
			}
			mu.Lock()
			cmds = append(cmds, cmd)
			mu.Unlock()
			json.NewEncoder(conn).Encode(reply(cmd))
			conn.Close()
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), cmds...)
	}
}

// commandNamed returns the last helper command with the given cmd name.
func commandNamed(cmds []map[string]any, name string) map[string]any {
	for i := len(cmds) - 1; i >= 0; i-- {
		if cmds[i]["cmd"] == name {
			return cmds[i]
		}
	}
	return nil
}

// app mode: the fake helper socket answers, so detectMode picks app.
// A helper refusal keeps its mount_failed reason and its message.
func TestSharesAppModeMountErrorMaps(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)

	sock := filepath.Join(dir, "helper.sock")
	saved := helper.HelperSocket
	helper.HelperSocket = sock
	defer func() { helper.HelperSocket = saved }()
	fakeHelper(t, sock, func(map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": map[string]any{"code": "bad_request", "message": "Permission denied for user test"}}
	})

	srv, _, _ := server(t, false)
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media","name":"MountTest","username":"user","password":"secret"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	if status, _ := created["status"].(string); status != "not_connected" {
		t.Errorf("expected not_connected, got %q", status)
	}
	reason, _ := created["reason"].(map[string]any)
	if reason == nil {
		t.Fatal("expected reason on mount failure")
	}
	if rCode, _ := reason["code"].(string); rCode != "mount_failed" {
		t.Errorf("expected mount_failed reason code, got %q", rCode)
	}
	if rMsg, _ := reason["message"].(string); !strings.Contains(rMsg, "Permission denied") {
		t.Errorf("expected Permission denied in message, got %q", rMsg)
	}
}

// POST in app mode calls Mount with the cifs kind, the //server/share address,
// the mount point and the password, and stores the share as connected.
func TestSharesPostAppModeCallsMount(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)

	sock := filepath.Join(dir, "helper.sock")
	saved := helper.HelperSocket
	helper.HelperSocket = sock
	defer func() { helper.HelperSocket = saved }()
	cmds := fakeHelper(t, sock, func(map[string]any) map[string]any {
		return map[string]any{"ok": true, "pid": 42}
	})

	srv, _, _ := server(t, false)
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media\\Movies","name":"My Share","username":"alice","password":"p@ss"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	gotCmd := commandNamed(cmds(), "mount")
	if gotCmd == nil {
		t.Fatal("helper was never asked to mount")
	}
	if kind, _ := gotCmd["kind"].(string); kind != "cifs" {
		t.Errorf("expected cifs kind, got %q", kind)
	}
	if mpt, _ := gotCmd["mountPoint"].(string); !strings.HasSuffix(mpt, "/my-share") {
		t.Errorf("expected mountPoint ending /my-share, got %q", mpt)
	}
	if pw, _ := gotCmd["password"].(string); pw != "p@ss" {
		t.Errorf("password not sent correctly: %q", pw)
	}
	if addr, _ := gotCmd["address"].(string); addr != "//nas/media/Movies" {
		t.Errorf("expected //nas/media/Movies, got %q", addr)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	if status, _ := created["status"].(string); status != "connected" {
		t.Errorf("expected connected status, got %q", status)
	}
}

// DELETE in app mode calls helper.Unmount on the share's mount point before
// removing the record.
func TestSharesAppModeDeleteCallsUnmount(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)

	sock := filepath.Join(dir, "helper.sock")
	saved := helper.HelperSocket
	helper.HelperSocket = sock
	defer func() { helper.HelperSocket = saved }()
	cmds := fakeHelper(t, sock, func(map[string]any) map[string]any {
		return map[string]any{"ok": true, "pid": 42}
	})

	srv, _, _ := server(t, false)
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\share","name":"ToDelete","username":"u","password":"p"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	id := fmt.Sprintf("%v", created["id"])

	code, resp, _ = do(t, nil, srv, call{method: "DELETE", path: "/api/v2/shares/" + id, ctype: "application/json"})
	if code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204 got %d: %s", code, resp)
	}
	gotUnmount := commandNamed(cmds(), "unmount")
	if gotUnmount == nil {
		t.Fatal("helper.Unmount was not called")
	}
	if mpt, _ := gotUnmount["mountPoint"].(string); !strings.HasSuffix(mpt, "/todelete") {
		t.Errorf("expected unmount path ending /todelete, got %q", mpt)
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
	setup, _ := created["setup"].(map[string]any)
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
	text := setupField(created, "text")
	removeText := setupField(created, "removeText")
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
	text := setupField(created, "text")
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
	text := setupField(created, "text")
	// sanitiseName drops the apostrophe: "Bob's Share" -> "bobs-share"
	if !strings.Contains(text, "bobs-share") {
		t.Errorf("expected sanitised name bobs-share: %q", text)
	}
	// The share path should be properly quoted
	if !strings.Contains(text, "'/var/lib/syp/shares/bobs-share'") {
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

// The helper's own refusals keep their mount_failed reason; mount errors are
// mapped by their text.
func TestMountErrReasonMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"helper bad_request", &helper.MountError{Code: "bad_request", Message: "x"}, "mount_failed"},
		{"helper not_allowed", &helper.MountError{Code: "not_allowed", Message: "no"}, "mount_failed"},
		{"permission denied", fmt.Errorf("mount error(13): Permission denied"), "auth_failed"},
		{"no such file", fmt.Errorf("mount error(2): No such file or directory"), "share_not_found"},
		{"no route", fmt.Errorf("mount error(113): No route to host"), "host_unreachable"},
		{"timed out", fmt.Errorf("mount: timed out"), "timeout"},
		{"helper socket down", fmt.Errorf("helper: dial unix: no such file"), "helper_missing"},
		{"anything else", fmt.Errorf("something odd"), "mount_failed"},
	}
	for _, c := range cases {
		var r struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(mountErrReason(c.err)), &r); err != nil {
			t.Fatalf("%s: reason is not JSON: %v", c.name, err)
		}
		if r.Code != c.code {
			t.Errorf("%s: expected %s, got %s", c.name, c.code, r.Code)
		}
	}
}

// detectMode: app when the helper socket answers, manual when it does not.
func TestDetectModeAppAndManualBySocket(t *testing.T) {
	t.Setenv("container", "")
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("running inside docker")
	}
	dir := t.TempDir()
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()

	helper.HelperSocket = filepath.Join(dir, "absent.sock")
	mode, platform := detectMode()
	if platform == "linux" && mode != "manual" {
		t.Errorf("no helper socket: expected manual, got %q", mode)
	}

	sock := filepath.Join(dir, "up.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	helper.HelperSocket = sock
	mode, platform = detectMode()
	if platform == "linux" && mode != "app" {
		t.Errorf("helper socket answers: expected app, got %q", mode)
	}
}

// The manual mount command shell-quotes the username once, so the value that
// mount.cifs receives is the username itself, with no stray quotes.
func TestManualSetupQuotesUsername(t *testing.T) {
	t.Setenv("container", "")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")

	sh := jobs.Share{Type: "smb", Address: `\\nas\media`, Name: "Media", Username: "o'brien x"}
	setup, ok := setupForMode(nil, sh).(map[string]any)
	if !ok {
		t.Skip("not in manual mode on this platform")
	}
	want := "sudo mkdir -p '/var/lib/syp/shares/media' && " +
		"sudo mount -t cifs '//nas/media' '/var/lib/syp/shares/media' -o " +
		shellQuote("username=o'brien x,vers=3.1.1,soft,nosuid,nodev,noexec,uid=syp,gid=syp,file_mode=0660,dir_mode=0770")
	if got, _ := setup["text"].(string); got != want {
		t.Errorf("manual text:\n got %s\nwant %s", got, want)
	}
}

// setupField reads one string field of a share's setup object from a response.
func setupField(created map[string]any, key string) string {
	setup, _ := created["setup"].(map[string]any)
	s, _ := setup[key].(string)
	return s
}

// A rename moves the mount point, so PATCH unmounts the old one before mounting at the new one.
func TestSharesAppModeRenameUnmountsOldMountPoint(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)

	sock := filepath.Join(dir, "helper.sock")
	saved := helper.HelperSocket
	helper.HelperSocket = sock
	defer func() { helper.HelperSocket = saved }()
	cmds := fakeHelper(t, sock, func(map[string]any) map[string]any {
		return map[string]any{"ok": true, "pid": 42}
	})

	srv, _, _ := server(t, false)
	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\share","name":"Old","username":"u","password":"p"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created map[string]any
	json.Unmarshal([]byte(resp), &created)
	id := fmt.Sprintf("%v", created["id"])

	code, resp, _ = do(t, nil, srv, call{method: "PATCH", path: "/api/v2/shares/" + id,
		body: `{"name":"New"}`, ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, resp)
	}
	var order []string
	for _, c := range cmds() {
		mp, _ := c["mountPoint"].(string)
		order = append(order, fmt.Sprintf("%v %s", c["cmd"], filepath.Base(mp)))
	}
	got := strings.Join(order, ", ")
	if !strings.Contains(got, "unmount old, mount new") {
		t.Errorf("helper calls %q: want an unmount of old, then a mount of new", got)
	}
}
