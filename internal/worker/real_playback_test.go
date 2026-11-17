// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A 24 fps file with AAC audio (48 kHz) plays back with no errors. Without -copyts, AAC's priming shifts the
// timestamps and the playback check reported "non monotonically increasing dts" on every such file.
func TestDecodeRun24fpsAACNoFalseAlarm(t *testing.T) {
	needTools(t)
	p := filepath.Join(t.TempDir(), "t.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", p)
	st, _ := realJob(t, p, 10)
	w := realWorker(st, media.OSExec{})
	if err := w.decodeRun(context.Background(), p, 0, 0, 0, 10, 24); err != nil {
		t.Fatalf("playback check false alarm: %v", err)
	}
	// The sampled path (a 5 s sample from 2 s) has the same false alarm without -copyts.
	if err := w.decodeRun(context.Background(), p, 0, 2, 5, 5, 24); err != nil {
		t.Fatalf("playback check false alarm on the sampled path: %v", err)
	}
}
