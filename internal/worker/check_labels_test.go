// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// logHas reports whether the job log has a line on the given step and stream containing text.
func (s *scenario) logHas(step int, stream, text string) bool {
	s.t.Helper()
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, l := range lines {
		if l.Step == step && l.Stream == stream && strings.Contains(l.Text, text) {
			return true
		}
	}
	return false
}

// hasLabels fails the test unless lines start with the labels, in order (the drawer's "What the check found"
// reads these words, so the labels are part of what the UI shows).
func hasLabels(t *testing.T, what string, lines []string, labels ...string) {
	t.Helper()
	if len(lines) < len(labels) {
		t.Fatalf("%s: got %q, want labels %q", what, lines, labels)
	}
	for i, want := range labels {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("%s line %d = %q, want prefix %q", what, i, lines[i], want)
		}
	}
}

// A playback refusal's detail: the check, where playback got to, and the errors.
func TestPlaybackRefusalLabels(t *testing.T) {
	err := playbackKept(nil, "[h264 @ 0x1] error while decoding MB 1 2", 0, 10, 10, 25)
	var ke *KeptError
	if !errors.As(err, &ke) {
		t.Fatalf("want a kept refusal, got %v", err)
	}
	hasLabels(t, "playback", ke.Detail, "Check: ", "Where: ", "Errors: ")
}

// A picture refusal's detail: the check, the scores, the alignment note, and the worst frames.
func TestPictureRefusalLabels(t *testing.T) {
	lines := ssimDetail(60, []segment{{0, 10}, {25, 10}, {50, 10}}, []placeScore{{at: 0, score: 0.5}},
		[]frameScore{{n: 1, score: 0.4}, {n: 2, score: 0.6}}, 25, 0.9)
	hasLabels(t, "picture", lines, "Check: ", "Scores: ", "Alignment: ", "Worst frames: ")
}

// A refusal the worker logs gets the encoder, original and new-file lines after its check lines.
func TestRefusalLogAddsEncoderAndFileLines(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Kept {
		t.Fatalf("status %s (error %q), want kept", j.Status, j.Error)
	}
	for _, want := range []string{"Check: ", "Encoder: ", "Original: ", "New file: "} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks a %q line", want)
		}
	}
}
