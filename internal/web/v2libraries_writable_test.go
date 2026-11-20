// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A library the app can't write to says so on the libraries API: writable false, with the message the Libraries
// screen shows. The field comes from the doctor's own access probe.
func TestV2LibraryWritableField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod cannot make a folder read-only on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only folder; run the test as another user")
	}
	srv, _, _, _ := libServer(t, false)
	// this test flips the folder's mode and reads it back, so it must not see a cached answer
	oldTTL := writeTTL
	writeTTL = 0
	t.Cleanup(func() { writeTTL = oldTTL })
	dir := filepath.Join(t.TempDir(), "movies")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// the app keeps the resolved path (on macOS the temp folder is under /var, a link to /private/var)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dir = resolved
	// add while the folder is writable, then make it read-only
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/libraries", body: `{"name":"Movies","paths":[` + strconv.Quote(dir) + `]}`, ctype: "application/json"})
	if code != 201 {
		t.Fatalf("add %d %s", code, body)
	}
	id := decode[v2Library](t, body).ID
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	get := func() (bool, string) {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + id})
		if code != 200 {
			t.Fatalf("get %d %s", code, body)
		}
		v := decode[struct {
			Writable          bool   `json:"writable"`
			UnwritableMessage string `json:"unwritableMessage"`
		}](t, body)
		return v.Writable, v.UnwritableMessage
	}

	if w, msg := get(); !w || msg != "" {
		t.Errorf("writable folder: writable %v, message %q; want true and no message", w, msg)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	w, msg := get()
	if w {
		t.Fatal("read-only folder: writable true, want false")
	}
	if want := "Can't write to this folder: " + dir + "."; msg != want {
		t.Errorf("message %q, want %q", msg, want)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if w, msg := get(); !w || strings.TrimSpace(msg) != "" {
		t.Errorf("writable again: writable %v, message %q", w, msg)
	}
}

// While a folder's first write probe is still running (a slow or hung mount), the list does not call the folder
// read-only: writable stays true and there is no message until the probe answers.
func TestV2LibraryUnansweredWriteProbeIsNotReadOnly(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	block := make(chan struct{})
	oldCheck, oldWait := writeCheck, writeTimeout
	writeCheck = func(string) bool { <-block; return false }
	writeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { close(block); writeCheck, writeTimeout = oldCheck, oldWait })

	dir := filepath.Join(t.TempDir(), "slow")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/libraries", body: `{"name":"Slow","paths":[` + strconv.Quote(dir) + `]}`, ctype: "application/json"})
	if code != 201 {
		t.Fatalf("add %d %s", code, body)
	}
	id := decode[v2Library](t, body).ID
	for _, path := range []string{"/api/v2/libraries/" + id, "/api/v2/libraries"} {
		code, body, _ := do(t, nil, srv, call{method: "GET", path: path})
		if code != 200 {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		if strings.Contains(body, "Can't write to this folder") || strings.Contains(body, `"writable":false`) {
			t.Errorf("GET %s while the probe is still running reports the folder read-only: %s", path, body)
		}
	}
}
