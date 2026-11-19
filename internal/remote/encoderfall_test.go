// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"slices"
	"strings"
	"testing"
)

// A damaged source is not the encoder's fault: no retry on the processor, the error goes back as it was.
func TestRemoteDamagedSourceIsNotAFallBack(t *testing.T) {
	x := &recordExec{msg: "Invalid data found when processing input", fail: func(args []string) bool { return slices.Contains(args, "h264_qsv") }}
	notes, _, err := convertH264(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err == nil {
		t.Fatal("want the damaged source to fail the convert")
	}
	if len(x.calls) != 1 {
		t.Errorf("ffmpeg calls %d, want 1 (no processor retry)", len(x.calls))
	}
	if len(notes) != 0 {
		t.Errorf("notes %q, want none", notes)
	}
}

// A graphics encode that converts reports its success to the server, which ends the worker's run of failures.
func TestRemoteGraphicsSuccessIsReported(t *testing.T) {
	x := &recordExec{}
	rec, err := convertH264Rec(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if rec.worked != 1 {
		t.Errorf("worked signals %d, want 1", rec.worked)
	}
}

// A graphics encode that fails, then the processor also fails: the encoder is not counted, no note says the
// processor converted it, and no success is reported.
func TestRemoteProcessorFailureIsNotAFallBack(t *testing.T) {
	x := &recordExec{fail: func(args []string) bool {
		return slices.Contains(args, "h264_qsv") || slices.Contains(args, "libx264")
	}}
	rec, err := convertH264Rec(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err == nil {
		t.Fatal("want the convert to fail when the processor fails too")
	}
	if len(x.calls) != 2 {
		t.Fatalf("ffmpeg calls %v: want h264_qsv then the processor", x.calls)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, n := range rec.notes {
		if strings.Contains(n, "converted on the processor") {
			t.Errorf("note %q: the processor did not convert it", n)
		}
	}
	if rec.worked != 0 {
		t.Errorf("worked signals %d, want 0", rec.worked)
	}
}
