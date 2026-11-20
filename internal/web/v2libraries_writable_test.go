// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
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
	dir := filepath.Join(t.TempDir(), "movies")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
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
	if want := "Can't write to this folder: " + dir + ". Change its owner/permissions."; msg != want {
		t.Errorf("message %q, want %q", msg, want)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if w, msg := get(); !w || strings.TrimSpace(msg) != "" {
		t.Errorf("writable again: writable %v, message %q", w, msg)
	}
}
