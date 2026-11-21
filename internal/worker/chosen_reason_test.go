// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Slice 11b follow-up (#547): a processor chosen because the graphics encoder is paused says why on the Chosen line.
func TestChosenLineSaysWhyAPausedEncoderWasSkipped(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	for i := 0; i < 3; i++ {
		fall(t, s, w)
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if n := logCount(t, s, "Chosen: x265 (processor) — hevc_nvenc is paused after three failures in a row. Settings:"); n != 1 {
		t.Errorf("Chosen line with its reason: %d, want 1", n)
	}
}

// Slice 11b follow-up (#547): a processor chosen because the encoder can't do this file says so on the Chosen line.
func TestChosenLineSaysWhyAnEncoderCantDoTheFile(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_videotoolbox", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if n := logCount(t, s, "Chosen: x265 (processor) — hevc_videotoolbox can't do this file. Settings:"); n != 1 {
		t.Errorf("Chosen line with its reason: %d, want 1", n)
	}
}
