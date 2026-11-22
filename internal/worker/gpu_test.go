// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// encodeCalls are the ffmpeg encodes the scenario ran, in order (the calls that carry an encoder).
func encodeCalls(s *scenario) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if strings.HasPrefix(c, "ffmpeg ") && strings.Contains(c, "-progress") && !strings.Contains(c, "-xerror") { // -xerror is the playback test, not an encode
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
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
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
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_videotoolbox", "" }
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
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "libx265") || strings.Contains(enc[0], "hevc_") {
		t.Fatalf("encodes: %v", enc)
	}
}

// The main encoder is graphics, but this plan's format encoder did not pass: the processor runs, and the reason is
// in the job log once.
func TestFormatEncoderThatDidNotPassIsLogged(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) {
		return "", "encoder h264_qsv didn't pass its test here"
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "libx265") || strings.Contains(enc[0], "_qsv") {
		t.Fatalf("encodes, want the processor alone:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "encoder h264_qsv didn't pass its test here; using the processor"); n != 1 {
		t.Errorf("reason line: %d, want 1", n)
	}
}

// A graphics failure converts again on the processor only when the encoder or ffmpeg failed, off the full GPU
// pipeline. On the full rung any decode-side failure steps down too (the card may not read this file), but a
// stop (the stall stop, a cancel, a lost lease), a full disk, a missing source or an unwritable output do not.
func TestGPUFailureOnlyRetriesAnEncoderError(t *testing.T) {
	w := &Worker{}
	ffErr := errors.New("ffmpeg exited 1")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		ctx    context.Context
		err    error
		stop   stopKind
		full   bool
		stderr string
		want   bool
	}{
		{"ffmpeg error", context.Background(), ffErr, stopNone, false, "something went wrong", true},
		{"encoder missing", context.Background(), ffErr, stopNone, false, "Unknown encoder 'h264_qsv'", true},
		{"no error", context.Background(), nil, stopNone, false, "", false},
		{"cancelled", cancelled, ffErr, stopNone, false, "", false},
		{"cancel error", context.Background(), context.Canceled, stopNone, false, "", false},
		{"stall stop", context.Background(), ffErr, stopStall, false, "", false},
		{"lease lost", context.Background(), errLeaseLost, stopNone, false, "", false},
		{"disk full", context.Background(), ffErr, stopNone, false, "No space left on device", false},
		{"damaged source", context.Background(), ffErr, stopNone, false, "Invalid data found when processing input", false},
		{"full: decode error steps down", context.Background(), ffErr, stopNone, true, "[hevc_qsv @ 0x1] Error while decoding stream #0:0: Function not implemented", true},
		{"full: invalid data steps down", context.Background(), ffErr, stopNone, true, "Invalid data found when processing input", true},
		{"full: disk full stops", context.Background(), ffErr, stopNone, true, "No space left on device", false},
		{"full: missing source stops", context.Background(), ffErr, stopNone, true, "No such file or directory", false},
		{"full: stall stop stops", context.Background(), ffErr, stopStall, true, "Error while decoding stream", false},
		{"full: cancelled stops", cancelled, ffErr, stopNone, true, "Error while decoding stream", false},
	}
	for _, c := range cases {
		w.stopped = c.stop
		if got := w.gpuFailed(c.ctx, c.err, c.stderr, c.full, hardwarePolicy{}); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
