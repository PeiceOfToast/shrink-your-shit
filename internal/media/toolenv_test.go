// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ffmpeg and ffprobe get the tool folder's lib folder on LD_LIBRARY_PATH when it exists, and nothing otherwise.
func TestToolEnvPutsTheLibFolderOnlyWhenItExists(t *testing.T) {
	defer SetToolDir("")
	t.Setenv("LD_LIBRARY_PATH", "/already/here")

	SetToolDir(t.TempDir()) // no lib folder yet
	if env := toolEnv("ffmpeg"); env != nil {
		t.Fatalf("without a lib folder the process must inherit the environment; got %d entries", len(env))
	}

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	SetToolDir(dir)
	env := toolEnv("ffmpeg")
	var got string
	for _, kv := range env {
		if strings.HasPrefix(kv, "LD_LIBRARY_PATH=") {
			got = strings.TrimPrefix(kv, "LD_LIBRARY_PATH=")
		}
	}
	if want := filepath.Join(dir, "lib") + ":/already/here"; got != want {
		t.Errorf("LD_LIBRARY_PATH = %q, want %q", got, want)
	}
}

// The service never gets the library folder: only ffmpeg and ffprobe do.
func TestToolEnvIsOnlyForFFmpegAndFFprobe(t *testing.T) {
	defer SetToolDir("")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	SetToolDir(dir)
	if env := toolEnv("sh"); env != nil {
		t.Errorf("a program other than ffmpeg or ffprobe got an environment: %d entries", len(env))
	}
}
