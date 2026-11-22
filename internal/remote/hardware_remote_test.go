// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Convert step's hardware choice on a remote worker (W7 round 2): the worker must honour it, not only the server.
const (
	noEncoderWords  = "Hardware only: no graphics encoder took this file, and this step never uses the processor. Your original was not touched."
	onlyFailedWords = "Hardware only: the graphics encoder failed, and this step never uses the processor. Your original was not touched."
	failFailedWords = "The graphics encoder failed, and this step is set to fail rather than convert on the processor. Your original was not touched."
)

// convertHW converts an H.264 plan with the Convert step's hardware choice on a worker whose report is caps, and
// returns the job-log notes, the encoder each converting report named, and the error.
func convertHW(t *testing.T, primary *string, caps []map[string]any, x *recordExec, hardware, hardwareFail string) (notes, encs []string, err error) {
	t.Helper()
	srv, rec := recordingServer(t)
	c := &Client{Server: srv.URL, Version: "test", Caps: caps}
	l := &Lease{ID: "L1", Primary: primary}
	cv := Converter{Exec: x, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir()}
	plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx264", CRF: 20, Preset: "medium", Hardware: hardware, HardwareFail: hardwareFail}}
	out := filepath.Join(t.TempDir(), "out.mkv")
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.notes, rec.encoders, err
}

// Hardware cpu: the graphics card is never asked, even when it passed its own test here.
func TestRemoteCPUOnlyNeverUsesTheCard(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertHW(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, "cpu", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || slices.Contains(x.calls[0], "h264_qsv") {
		t.Errorf("ffmpeg calls %v: want the processor alone", x.calls)
	}
	if !slices.ContainsFunc(notes, func(s string) bool { return strings.Contains(s, "CPU only, as the Convert step says") }) {
		t.Errorf("notes %q: want the CPU-only line", notes)
	}
}

// Hardware only with no graphics encoder that can take the file: refused before ffmpeg runs, with the plain words.
func TestRemoteOnlyWithNoCardRefusesBeforeFFmpeg(t *testing.T) {
	x := &recordExec{}
	_, _, err := convertHW(t, ptr("hevc_qsv"), nil, x, "only", "")
	if err == nil || err.Error() != noEncoderWords {
		t.Errorf("err %v: want %q", err, noEncoderWords)
	}
	if len(x.calls) != 0 {
		t.Errorf("ffmpeg ran %d times: want none, the processor never converts under only", len(x.calls))
	}
}

// Hardware only and the card fails: the job stops with the plain words and the processor does not convert.
func TestRemoteOnlyStopsWhenTheCardFails(t *testing.T) {
	x := &recordExec{fail: func(args []string) bool { return slices.Contains(args, "h264_qsv") }}
	_, _, err := convertHW(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, "only", "")
	if err == nil || err.Error() != onlyFailedWords {
		t.Errorf("err %v: want %q", err, onlyFailedWords)
	}
	if len(x.calls) != 1 || slices.ContainsFunc(x.calls, func(a []string) bool { return slices.Contains(a, "libx264") }) {
		t.Errorf("ffmpeg calls %v: want the card's call only", x.calls)
	}
}

// Hardware fail and the card fails: no step down to the processor, with the plain words.
func TestRemoteFailStopsWhenTheCardFails(t *testing.T) {
	x := &recordExec{fail: func(args []string) bool { return slices.Contains(args, "h264_qsv") }}
	_, _, err := convertHW(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, "auto", "fail")
	if err == nil || err.Error() != failFailedWords {
		t.Errorf("err %v: want %q", err, failFailedWords)
	}
	if len(x.calls) != 1 {
		t.Errorf("ffmpeg calls %v: want the card's call only", x.calls)
	}
}

// The default (auto, next): a card that fails steps down to the processor, and the job converts.
func TestRemoteAutoStepsDownWhenTheCardFails(t *testing.T) {
	x := &recordExec{fail: func(args []string) bool { return slices.Contains(args, "h264_qsv") }}
	_, _, err := convertHW(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 2 || !slices.Contains(x.calls[1], "libx264") {
		t.Errorf("ffmpeg calls %v: want the card, then the processor", x.calls)
	}
}

// A policy stop is reported with its plain words and not retried: code hardware_choice, not the generic failure.
func TestHardwareChoiceFailureReportIsPlainAndNotRetried(t *testing.T) {
	body := failureReport(&hardwareStop{words: onlyFailedWords}, "", nil)
	if body["code"] != "hardware_choice" {
		t.Errorf("code %v: want hardware_choice", body["code"])
	}
	if body["message"] != onlyFailedWords {
		t.Errorf("message %v: want the plain words", body["message"])
	}
}
