// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"path/filepath"
	"testing"
)

// The tool folder is used for ffmpeg and ffprobe only; anything else stays a PATH name, and no folder means PATH.
func TestToolDirServesFFmpegAndFFprobeOnly(t *testing.T) {
	defer SetToolDir("")
	if got := tool("ffmpeg"); got != "ffmpeg" {
		t.Fatalf("no folder: tool(ffmpeg) = %q, want the PATH name", got)
	}
	SetToolDir("/opt/syp/ffmpeg")
	if got, want := tool("ffmpeg"), filepath.Join("/opt/syp/ffmpeg", "ffmpeg"); got != want {
		t.Errorf("tool(ffmpeg) = %q, want %q", got, want)
	}
	if got, want := tool("ffprobe"), filepath.Join("/opt/syp/ffmpeg", "ffprobe"); got != want {
		t.Errorf("tool(ffprobe) = %q, want %q", got, want)
	}
	if got := tool("sh"); got != "sh" {
		t.Errorf("tool(sh) = %q, want the PATH name", got)
	}
}
