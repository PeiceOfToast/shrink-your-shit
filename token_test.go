// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

func TestTokenCommandPrintsTokenAndSignInAddress(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte("abc_DEF-123\n"), 0o600)
	t.Setenv("SYP_DATA", dir)
	t.Setenv("SYP_ADDR", "192.0.2.7:9000")
	var out, errOut bytes.Buffer
	if rc := tokenCommand(&out, &errOut); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errOut.String())
	}
	want := "Access token: abc_DEF-123\nSign-in address: http://192.0.2.7:9000/?token=abc_DEF-123\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestTokenCommandWithNoToken(t *testing.T) {
	t.Setenv("SYP_DATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	if _, err := os.Stat("/var/lib/shrink-your-shit/" + web.TokenFile); err == nil {
		t.Skip("an installed service's token exists on this machine")
	}
	var out, errOut bytes.Buffer
	if rc := tokenCommand(&out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "No access token") {
		t.Errorf("rc %d, %q", rc, errOut.String())
	}
}

func TestSignInURL(t *testing.T) {
	if got := signInURL("127.0.0.1:8484", "t"); got != "http://127.0.0.1:8484/?token=t" {
		t.Error(got)
	}
	if got := signInURL("[::1]:8484", "t"); got != "http://[::1]:8484/?token=t" {
		t.Error(got)
	}
	for _, all := range []string{"0.0.0.0:8484", ":8484", "[::]:8484"} {
		if got := signInURL(all, "t"); strings.Contains(got, "0.0.0.0") || strings.Contains(got, "[::]") || !strings.HasSuffix(got, ":8484/?token=t") {
			t.Errorf("%s -> %s: want this machine's address", all, got)
		}
	}
}
