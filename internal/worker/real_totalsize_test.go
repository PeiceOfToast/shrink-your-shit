// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The Dashboard's saving estimate projects a converting file's size from the bytes ffmpeg has written
// (total_size). That needs ffmpeg to report it for an output file: if it never does (or says N/A), every
// running file falls back to the done jobs' ratio, so this pins the assumption to real ffmpeg.
func TestRealProgressReportsTheNewFilesSize(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=640x360:rate=30", "-f", "lavfi", "-i", "sine=duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "8M", "-c:a", "aac", in)
	out := filepath.Join(dir, "out.mkv")
	args := []string{"-y", "-nostdin", "-v", "error", "-progress", "pipe:1", "-nostats", "-i", in,
		"-c:v", "libx265", "-preset", "ultrafast", "-crf", "30", "-c:a", "copy", out}
	var last int64
	reports := 0
	if _, err := media.Transcode(context.Background(), media.OSExec{}, "ffmpeg", args, 6, func(p media.Progress) {
		if p.TotalSize > 0 {
			reports++
			last = p.TotalSize
		}
	}); err != nil {
		t.Fatal(err)
	}
	if reports == 0 || last == 0 {
		t.Fatalf("ffmpeg never said total_size for an output file (%d reports)", reports)
	}
}
