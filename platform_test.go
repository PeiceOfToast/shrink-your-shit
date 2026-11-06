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
