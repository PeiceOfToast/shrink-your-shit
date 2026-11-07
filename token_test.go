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

func TestTokenCommandFindsTheInstalledAddress(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte("tok\n"), 0o600)
	t.Setenv("SYP_DATA", dir)
	t.Setenv("SYP_ADDR", "")
	home, la := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", la)
	if _, err := os.Stat(linuxEnvFile); err == nil {
		t.Skip("this machine has an installed Linux service's settings file")
	}
	run := func() string {
		var out, errOut bytes.Buffer
		if rc := tokenCommand(&out, &errOut); rc != 0 {
			t.Fatalf("rc %d %s", rc, errOut.String())
		}
		return out.String()
	}
	// nothing says where it listens: a placeholder, never a wrong address
	if got := run(); !strings.Contains(got, "http://<this computer's address>:8484/?token=tok") {
		t.Errorf("no address known:\n%s", got)
	}
	// the Windows launcher
	os.MkdirAll(filepath.Join(la, "shrink-your-shit"), 0o755)
	os.WriteFile(filepath.Join(la, "shrink-your-shit", "launch.vbs"), []byte("Set env = sh.Environment(\"PROCESS\")\r\nenv(\"SYP_ADDR\") = \"192.0.2.9:9001\"\r\n"), 0o644)
	if got := run(); !strings.Contains(got, "http://192.0.2.9:9001/?token=tok") {
		t.Errorf("Windows launcher address:\n%s", got)
	}
	// the macOS LaunchAgent comes first
	os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755)
	os.WriteFile(filepath.Join(home, "Library", "LaunchAgents", "dev.shrink-your-shit.plist"),
		[]byte("    <key>SYP_ADDR</key><string>192.0.2.8:9002</string>\n"), 0o644)
	if got := run(); !strings.Contains(got, "http://192.0.2.8:9002/?token=tok") {
		t.Errorf("macOS LaunchAgent address:\n%s", got)
	}
}

// The installed service's data folder, on each install layout (restore --force and token both use it).
func TestInstalledDataDirOnEachLayout(t *testing.T) {
	home, la := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", la)
	t.Setenv("SYP_DATA", "")
	none := map[string]string{}
	if got := installedDataDir(none); got != "" {
		t.Errorf("nothing installed: %q, want none (never a guess)", got)
	}
	// the Windows launcher sets SYP_DATA only for itself; the folder is read from launch.vbs
	os.MkdirAll(filepath.Join(la, "shrink-your-shit"), 0o755)
	os.WriteFile(filepath.Join(la, "shrink-your-shit", "launch.vbs"),
		[]byte("Set env = sh.Environment(\"Process\")\r\nenv(\"SYP_ADDR\") = \"127.0.0.1:8484\"\r\nenv(\"SYP_DATA\") = \"C:\\Users\\a b\\AppData\\Local\\shrink-your-shit\\data\"\r\n"), 0o644)
	if got := installedDataDir(none); got != `C:\Users\a b\AppData\Local\shrink-your-shit\data` {
		t.Errorf("Windows launcher: %q", got)
	}
	// the macOS LaunchAgent, as install.sh writes it (escaped)
	os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755)
	os.WriteFile(filepath.Join(home, "Library", "LaunchAgents", "dev.shrink-your-shit.plist"),
		[]byte("    <key>SYP_DATA</key><string>/Users/a/Library/Application Support/R&amp;D</string>\n"), 0o644)
	if got := installedDataDir(none); got != "/Users/a/Library/Application Support/R&D" {
		t.Errorf("macOS LaunchAgent: %q", got)
	}
	// the Linux settings file
	if got := installedDataDir(map[string]string{"SYP_DATA": "/var/lib/shrink-your-shit"}); got != "/var/lib/shrink-your-shit" {
		t.Errorf("Linux settings file: %q", got)
	}
	// SYP_DATA in this process comes first
	t.Setenv("SYP_DATA", "/srv/own")
	if got := installedDataDir(map[string]string{"SYP_DATA": "/var/lib/shrink-your-shit"}); got != "/srv/own" {
		t.Errorf("SYP_DATA: %q", got)
	}
}
