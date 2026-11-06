// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// defaultDataDir is where the database lives unless -data or SYP_DATA says
// otherwise: the per-user config folder (~/.config on Linux,
// ~/Library/Application Support on macOS, %AppData% on Windows).
func defaultDataDir() string {
	if v := os.Getenv("SYP_DATA"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "shrink-your-shit")
	}
	return "./data"
}

// resolveTool finds ffmpeg or ffprobe. An explicit path (flag or env var) must
// exist; otherwise look on PATH (LookPath adds .exe on Windows), then next to
// our own executable, which is where a Windows/portable bundle puts them.
func resolveTool(name, explicit string) (string, error) {
	if explicit != "" {
		if p, err := exec.LookPath(explicit); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("%q does not exist or is not runnable", explicit)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if self, err := os.Executable(); err == nil {
		if p, err := exec.LookPath(filepath.Join(filepath.Dir(self), name)); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("can't find %s. %s Or set its location with -%s or the SYP_%s environment variable",
		name, installHint(), name, upper(name))
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func installHint() string {
	switch runtime.GOOS {
	case "windows":
		return "Install ffmpeg (winget install ffmpeg) or put ffmpeg.exe and ffprobe.exe next to this program."
	case "darwin":
		return "Install ffmpeg (brew install ffmpeg)."
	default:
		return "Install ffmpeg (Debian/Ubuntu/WSL2: sudo apt install ffmpeg)."
	}
}
