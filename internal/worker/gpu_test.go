// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// encodeCalls are the ffmpeg encodes the scenario ran, in order (the calls that carry an encoder).
func encodeCalls(s *scenario) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if strings.HasPrefix(c, "ffmpeg ") && strings.Contains(c, "-progress") {
			out = append(out, c)
		}
	}
	return out
}

// logCount counts the job's log lines that contain sub.
func logCount(t *testing.T, s *scenario, sub string) int {
	t.Helper()
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lines {
		if strings.Contains(l.Text, sub) {
			n++
		}
	}
	return n
}

// A graphics encode that fails converts again on the processor: the job completes, the processor's run
// comes second, and the log says so once.
func TestGPUFailureConvertsOnTheProcessor(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_nvenc"
	w := s.worker(Replace)
	w.Encoder = func(string) string { return "hevc_nvenc" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 2 || !strings.Contains(enc[0], "hevc_nvenc") || strings.Contains(enc[1], "hevc_nvenc") || !strings.Contains(enc[1], "libx265") {
		t.Fatalf("encodes, want the GPU then the processor:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "hevc_nvenc failed ("); n != 1 {
		t.Errorf("failure line: %d, want 1", n)
	}
	if n := logCount(t, s, "converted on the processor"); n != 1 {
		t.Errorf("converted-on-processor line: %d, want 1", n)
	}
}

// An encoder that can't do this file runs the processor first try, and says so once.
func TestUnsupportedEncoderUsesProcessorFirst(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string) string { return "hevc_videotoolbox" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "libx265") {
		t.Fatalf("encodes: %v", enc)
	}
	if n := logCount(t, s, "encoder hevc_videotoolbox can't do this file; using the processor"); n != 1 {
		t.Errorf("can't-do line: %d, want 1", n)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("status %s", j.Status)
	}
}

// The processor chosen: no graphics encoder is asked for, and the encode is the processor's alone.
func TestCPUChoiceNeverAsksForAGraphicsEncoder(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string) string { return "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "libx265") || strings.Contains(enc[0], "hevc_") {
		t.Fatalf("encodes: %v", enc)
	}
}

// A graphics failure that is a stop (the stall stop, a cancel, a lost lease) is not converted again.
func TestGPUFailureOnlyRetriesAnFFmpegError(t *testing.T) {
	w := &Worker{}
	ffErr := errors.New("ffmpeg exited 1")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		stop stopKind
		want bool
	}{
		{"ffmpeg error", context.Background(), ffErr, stopNone, true},
		{"no error", context.Background(), nil, stopNone, false},
		{"cancelled", cancelled, ffErr, stopNone, false},
		{"cancel error", context.Background(), context.Canceled, stopNone, false},
		{"stall stop", context.Background(), ffErr, stopStall, false},
		{"lease lost", context.Background(), errLeaseLost, stopNone, false},
	}
	for _, c := range cases {
		w.stopped = c.stop
		if got := w.gpuFailed(c.ctx, c.err); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
