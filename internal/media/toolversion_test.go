// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"strings"
	"testing"
)

// The version is the word after "version " on the tool's first line, and only for the tool that said it.
func TestVersionOf(t *testing.T) {
	cases := []struct {
		name, line, want string
		ok               bool
	}{
		{"ffmpeg", "ffmpeg version 7.0.2 Copyright (c) 2000-2024 the FFmpeg developers", "7.0.2", true},
		{"ffprobe", "ffprobe version n7.1-12-g2e5e3f0 Copyright (c) 2007-2024 the FFmpeg developers", "n7.1-12-g2e5e3f0", true},
		{"ffmpeg", "ffprobe version 7.0.2 Copyright (c) 2007-2024 the FFmpeg developers", "", false},
		{"ffmpeg", "ffmpeg has no version here", "", false},
		{"ffmpeg", "", "", false},
	}
	for _, tc := range cases {
		got, ok := versionOf(tc.name, tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("versionOf(%q, %q) = %q, %v; want %q, %v", tc.name, tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

// A tool the folder does not hold is named in the error rather than a bare lookup failure.
func TestToolVersionMissingTool(t *testing.T) {
	defer SetToolDir("")
	SetToolDir(t.TempDir())
	_, err := ToolVersion(context.Background(), "ffmpeg")
	if err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
		t.Errorf("ToolVersion of an absent ffmpeg = %v, want it to name ffmpeg", err)
	}
	if _, err := ToolVersion(context.Background(), "ffplay"); err == nil {
		t.Error("ToolVersion(ffplay) = nil error; want it refused")
	}
}
