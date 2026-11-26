// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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
	if rc := tokenCommand(nil, &out, &errOut); rc != 0 {
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
	if rc := tokenCommand(nil, &out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "No access token") {
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
		if rc := tokenCommand(nil, &out, &errOut); rc != 0 {
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

func TestTokenArgsBothOrders(t *testing.T) {
	cases := []struct {
		args []string
		want []string
		ok   bool
	}{
		{[]string{"token"}, []string{}, true},
		{[]string{"token", "-data", "/d"}, []string{"-data", "/d"}, true},
		{[]string{"-data", "/d", "token"}, []string{"-data", "/d"}, true},
		{[]string{"--data", "/d", "token"}, []string{"-data", "/d"}, true},
		{[]string{"-data=/d", "token"}, []string{"-data=/d"}, true},
		{[]string{"-data", "/d"}, nil, false},
		{[]string{"-addr", ":8484"}, nil, false},
		{nil, nil, false},
	}
	for _, c := range cases {
		got, ok := tokenArgs(c.args)
		if ok != c.ok || strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("tokenArgs(%q) = %q, %v; want %q, %v", c.args, got, ok, c.want, c.ok)
		}
	}
}

// -data reads that folder and no other, whichever argument order brought it here.
func TestTokenWithDataReadsOnlyThatFolder(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte("from-data\n"), 0o600)
	os.WriteFile(filepath.Join(other, web.TokenFile), []byte("from-env\n"), 0o600)
	t.Setenv("SYP_DATA", other)
	t.Setenv("SYP_ADDR", "192.0.2.7:9000")
	for _, args := range [][]string{{"-data", dir}, {"-data=" + dir}} {
		var out, errOut bytes.Buffer
		if rc := tokenCommand(args, &out, &errOut); rc != 0 {
			t.Fatalf("%v: rc %d: %s", args, rc, errOut.String())
		}
		if !strings.Contains(out.String(), "Access token: from-data\n") {
			t.Errorf("%v: got\n%s", args, out.String())
		}
	}
}

// A folder with no token file says so, names the folder, and never claims no token is set.
func TestTokenWithDataMissingFileNamesTheFolder(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir}, &out, &errOut); rc != 1 {
		t.Fatalf("rc %d", rc)
	}
	msg := errOut.String()
	if !strings.Contains(msg, dir) || !strings.Contains(msg, web.TokenFile) || strings.Contains(msg, "No access token is set") {
		t.Errorf("message: %q", msg)
	}
}

// A read error that is not a permission problem is reported as itself, not as "no token".
func TestTokenWithDataReportsTheRealReadError(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, web.TokenFile), 0o755) // a folder where the file should be
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir}, &out, &errOut); rc != 1 {
		t.Fatalf("rc %d", rc)
	}
	msg := errOut.String()
	if !strings.Contains(msg, "Cannot read the access token in "+dir) || strings.Contains(msg, "No access token") {
		t.Errorf("message: %q", msg)
	}
}

func TestTokenWithDataEmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte(" \n"), 0o600)
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir}, &out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "is empty") {
		t.Errorf("rc %d: %q", rc, errOut.String())
	}
}

// Denied access gets the sudo hint for this folder. Root reads anything, so the test needs another user.
func TestTokenWithDataPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user to be denied")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, web.TokenFile)
	os.WriteFile(p, []byte("tok\n"), 0o000)
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir}, &out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "sudo shrink-your-shit token -data "+dir) {
		t.Errorf("rc %d: %q", rc, errOut.String())
	}
}

func TestTokenUsageErrorIsExit2(t *testing.T) {
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"extra"}, &out, &errOut); rc != 2 {
		t.Errorf("rc %d: %q", rc, errOut.String())
	}
}

// With -data, the sign-in address is never the installed service's: that address goes with another token.
func TestTokenWithDataDoesNotUseTheInstalledAddress(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte("from-data\n"), 0o600)
	t.Setenv("SYP_ADDR", "192.0.2.7:8484")
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir}, &out, &errOut); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errOut.String())
	}
	if strings.Contains(out.String(), "192.0.2.7") || !strings.Contains(out.String(), "http://<this computer's address>:8484/?token=from-data") {
		t.Errorf("got\n%s", out.String())
	}
}

// -addr gives the address of the server the -data folder belongs to.
func TestTokenWithDataAddrBuildsTheAddress(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, web.TokenFile), []byte("from-data\n"), 0o600)
	t.Setenv("SYP_ADDR", "192.0.2.7:8484")
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-data", dir, "-addr", "192.0.2.5:9100"}, &out, &errOut); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errOut.String())
	}
	if want := "Sign-in address: http://192.0.2.5:9100/?token=from-data\n"; !strings.Contains(out.String(), want) {
		t.Errorf("got\n%s\nwant it to contain %q", out.String(), want)
	}
}

// -addr only means something with -data; without it, the token command is as before (no -addr flag).
func TestTokenAddrNeedsData(t *testing.T) {
	var out, errOut bytes.Buffer
	if rc := tokenCommand([]string{"-addr", ":9100"}, &out, &errOut); rc != 2 {
		t.Errorf("rc %d: %q", rc, errOut.String())
	}
}

// A service started with its own -data folder gets the command that names it; the default folder gets the plain one.
func TestTokenHintNamesAFolderTheSearchMisses(t *testing.T) {
	if got := tokenHint(defaultDataDir()); got != "shrink-your-shit token" {
		t.Errorf("default folder: want the plain command, got %q", got)
	}
	dir := filepath.Join(t.TempDir(), "my data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := `shrink-your-shit token -data "` + dir + `"`
	if got := tokenHint(dir); got != want {
		t.Errorf("own folder: want %q, got %q", want, got)
	}
}
