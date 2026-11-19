// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// fall records a graphics-encoder failure for the scenario's job on its worker, as a fall-back does.
func fall(t *testing.T, s *scenario, w *Worker) {
	t.Helper()
	if _, err := s.store.EncoderFell(s.id, w.ID, "hevc_nvenc", "Unknown encoder 'hevc_nvenc'"); err != nil {
		t.Fatal(err)
	}
}

// A worker whose graphics encoder is paused does not run it: the file goes to the processor first time.
func TestPausedGraphicsEncoderIsNotUsed(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string) (string, string) { return "hevc_nvenc", "" }
	for i := 0; i < 3; i++ {
		fall(t, s, w)
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || strings.Contains(enc[0], "hevc_nvenc") || !strings.Contains(enc[0], "libx265") {
		t.Fatalf("encodes, want the processor only:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "is paused until"); n != 1 {
		t.Errorf("paused line: %d, want 1", n)
	}
}

// A graphics success ends a run of failures: two falls, a success, then two more falls do not pause the encoder.
func TestGraphicsSuccessEndsARunOfFalls(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string) (string, string) { return "hevc_nvenc", "" }
	fall(t, s, w)
	fall(t, s, w)
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "hevc_nvenc") {
		t.Fatalf("encodes, want the graphics encoder once:\n%s", strings.Join(enc, "\n"))
	}
	fall(t, s, w)
	fall(t, s, w)
	if _, paused, err := s.store.EncoderPaused(w.ID); err != nil || paused {
		t.Errorf("paused %v, %v after a success and two falls: want not paused", paused, err)
	}
}

// When the processor fails too, the encoder was not the fault: no fall-back is recorded, nothing counts toward a
// pause, and the job fails as it did before fall-backs.
func TestProcessorFailureIsNotAFallBack(t *testing.T) {
	s := newScenario(t)
	s.failAllEncodes = true
	w := s.worker(Replace)
	w.Encoder = func(string) (string, string) { return "hevc_nvenc", "" }
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Fatal("RunOne: want the failed conversion's error")
	}
	if j := s.job(); j.Status == jobs.Done {
		t.Fatalf("status %s: the job should fail when the processor fails too", j.Status)
	}
	if enc := encodeCalls(s); len(enc) != 2 || !strings.Contains(enc[1], "libx265") {
		t.Fatalf("encodes, want the graphics encoder then the processor:\n%s", strings.Join(enc, "\n"))
	}
	falls, err := s.store.EncoderFallbacksSince(time.Unix(0, 0))
	if err != nil || len(falls) != 0 {
		t.Errorf("fall-backs %+v, %v: want none", falls, err)
	}
	if enc, _, err := s.store.EncoderLastFell(w.ID); err != nil || enc != "" {
		t.Errorf("last fall %q, %v: want none", enc, err)
	}
	if n := logCount(t, s, "converted on the processor"); n != 0 {
		t.Errorf("converted-on-processor line: %d, want 0", n)
	}
}
