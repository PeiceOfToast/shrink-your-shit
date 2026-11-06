// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	t.Setenv("SYP_DATA", "")
	if d := defaultDataDir(); !strings.HasSuffix(d, "shrink-your-shit") && d != "./data" {
		t.Errorf("unexpected default %q", d)
	}
	t.Setenv("SYP_DATA", "/custom")
	if d := defaultDataDir(); d != "/custom" {
		t.Errorf("env override ignored: %q", d)
	}
}

// fakeTool creates an executable file that LookPath will accept on this OS.
func fakeTool(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveToolExplicit(t *testing.T) {
	dir := t.TempDir()
	p := fakeTool(t, dir, "myffmpeg")
	got, err := resolveTool("ffmpeg", p)
	if err != nil || got != p {
		t.Errorf("%q %v", got, err)
	}
	if _, err := resolveTool("ffmpeg", filepath.Join(dir, "nope")); err == nil {
		t.Error("missing explicit path must be an error, not a silent PATH fallback")
	}
}

func TestResolveToolOnPath(t *testing.T) {
	dir := t.TempDir()
	p := fakeTool(t, dir, "syp-fake-tool")
	t.Setenv("PATH", dir)
	got, err := resolveTool("syp-fake-tool", "")
	if err != nil || got != p {
		t.Errorf("%q %v", got, err)
	}
}

func TestResolveToolMissingIsClear(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := resolveTool("syp-no-such-tool", "")
	if err == nil || !strings.Contains(err.Error(), "SYP_SYP-NO-SUCH-TOOL") || !strings.Contains(err.Error(), "-syp-no-such-tool") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("SYP_TEST_X", "")
	if envOr("SYP_TEST_X", "d") != "d" {
		t.Error("empty should fall back")
	}
	t.Setenv("SYP_TEST_X", "v")
	if envOr("SYP_TEST_X", "d") != "v" {
		t.Error("set value should win")
	}
}

func TestLibrariesFromEnv(t *testing.T) {
	a, b := filepath.Join("x", "a b"), filepath.Join("y", "c")
	t.Setenv("SYP_LIBRARIES", a+string(filepath.ListSeparator)+string(filepath.ListSeparator)+b)
	got := librariesFromEnv()
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("%q", got)
	}
	t.Setenv("SYP_LIBRARIES", "")
	if len(librariesFromEnv()) != 0 {
		t.Error("unset should give none")
	}
}

func TestExposedToNetwork(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8484": false, "localhost:8484": false, "[::1]:8484": false,
		"0.0.0.0:8484": true, ":8484": true, "192.168.1.5:8484": true, "[::]:8484": true,
	} {
		if got := exposedToNetwork(addr); got != want {
			t.Errorf("%s: got %v want %v", addr, got, want)
		}
	}
}
