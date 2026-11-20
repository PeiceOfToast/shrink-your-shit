// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// decodeHook says the family decodes codec at the bit depth, and records what it was asked.
func decodeHook(ok bool, asked *string) func(context.Context, string, string, bool) bool {
	return func(_ context.Context, fam, codec string, tenBit bool) bool {
		*asked = fam + " " + codec
		if tenBit {
			*asked += " 10-bit"
		}
		return ok
	}
}

// A family that decodes the source gets the full GPU pipeline: decode and encode on the device, one encode.
func TestFullGPUPipelineWhenTheFamilyDecodes(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if asked != "qsv h264" {
		t.Errorf("decode probe asked %q, want qsv h264", asked)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "-hwaccel qsv") || strings.Contains(enc[0], "hwupload") {
		t.Fatalf("encodes, want one full GPU encode:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "decoding and encoding on the graphics card (full GPU pipeline)"); n != 1 {
		t.Errorf("pipeline line: %d, want 1", n)
	}
}

// A 10-bit SDR source asks the card for a 10-bit decode on the full rung too: the same need10 rule as the
// encoder's choice, so the pipeline that runs is one the output check will accept (a 10-bit source is kept at
// its depth).
func TestFullGPUAsksForATenBitDecodeOnATenBitSource(t *testing.T) {
	s := newScenario(t)
	s.srcSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("h264", 640, 360, "yuv420p10le"), aud("aac", "eng", 2)}}
	s.outSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("hevc", 640, 360, "yuv420p10le"), aud("aac", "eng", 2)}}
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if asked != "qsv h264 10-bit" {
		t.Errorf("decode probe asked %q, want qsv h264 10-bit", asked)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "-hwaccel qsv") {
		t.Fatalf("encodes, want one full GPU encode:\n%s", strings.Join(enc, "\n"))
	}
	// the source's depth reaches the arguments too: main10, and no -pix_fmt (the full pipeline's frames come
	// from the decoder)
	if !strings.Contains(enc[0], "-profile:v:0 main10") || strings.Contains(enc[0], "-pix_fmt:v:0") {
		t.Errorf("a 10-bit source's full-GPU encode must ask for main10 and take its frames from the decoder:\n%s", enc[0])
	}
}

// Forced unsupported codec: the family does not decode H.264 here, so the processor decodes and the graphics
// encoder encodes (no full pipeline), and the log says why.
func TestUnsupportedDecodeKeepsTheProcessorDecoding(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(false, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "hevc_qsv") || strings.Contains(enc[0], "-hwaccel") {
		t.Fatalf("encodes, want the processor decoding for hevc_qsv:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "qsv can't decode H.264 here; the processor decodes"); n != 1 {
		t.Errorf("reason line: %d, want 1", n)
	}
}

// A full-pipeline failure steps down to the processor decoding for the same encoder. It is not counted toward
// the encoder's pause, and the file converts on the graphics encoder.
// The card's own decode error on the full rung (the card cannot read this file) steps down to the processor
// decoding and succeeds, instead of failing the job as a damaged source.
func TestFullGPUDecodeErrorStepsDown(t *testing.T) {
	s := newScenario(t)
	s.failFullGPU = true
	s.fullGPUStderr = "[hevc_qsv @ 0x1] Error while decoding stream #0:0: Function not implemented\n"
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q: a decode error on the full rung must step down, not fail the file", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 2 || !strings.Contains(enc[0], "-hwaccel") || strings.Contains(enc[1], "-hwaccel") {
		t.Fatalf("encodes, want the full pipeline then the processor decoding:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "full GPU pipeline failed"); n != 1 {
		t.Errorf("step-down line: %d, want 1", n)
	}
	if _, paused, err := w.Queue.EncoderPaused(w.ID); err != nil || paused {
		t.Errorf("paused %v (err %v), want not paused after a step-down", paused, err)
	}
}

func TestFullGPUFailureStepsDownWithoutCountingPause(t *testing.T) {
	s := newScenario(t)
	s.failFullGPU = true
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 2 || !strings.Contains(enc[0], "-hwaccel") || strings.Contains(enc[1], "-hwaccel") || !strings.Contains(enc[1], "hevc_qsv") {
		t.Fatalf("encodes, want the full pipeline then the processor decoding for hevc_qsv:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "full GPU pipeline failed"); n != 1 {
		t.Errorf("step-down line: %d, want 1", n)
	}
	if n := logCount(t, s, "converted on the processor"); n != 0 {
		t.Errorf("converted-on-processor line: %d, want 0 (the graphics encoder converted it)", n)
	}
	if _, paused, err := w.Queue.EncoderPaused(w.ID); err != nil || paused {
		t.Errorf("paused %v (err %v), want not paused after a step-down", paused, err)
	}
}
