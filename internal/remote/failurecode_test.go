// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Slice 15: a remote worker's failed conversion carries the file-type refusal's code, so the server stores
// settings_rejected; a bare header stop carries none, and keeps today's code.
func TestFailureReportCarriesTheSettingsRejectedCode(t *testing.T) {
	s4 := "[webm @ 0x1] Only VP8 or VP9 or AV1 video and Vorbis or Opus audio and WebVTT subtitles are supported for WebM.\n" +
		"[out#0/webm @ 0x2] Could not write header (incorrect codec parameters ?): Invalid argument"
	body := failureReport(errors.New("ffmpeg exited 1"), s4, []string{"ffmpeg", "-i", "in.mkv", "out.webm"})
	if body["code"] != jobs.ReasonSettingsRejected {
		t.Errorf("code %v, want %s", body["code"], jobs.ReasonSettingsRejected)
	}
	if body["ok"] != false || body["message"] != "ffmpeg exited 1" || body["stderr"] != s4 {
		t.Errorf("the report's words: %v", body)
	}
	if args, _ := body["args"].([]string); len(args) != 4 || args[3] != "out.webm" {
		t.Errorf("the arguments that ran: %v", body["args"])
	}
	bare := failureReport(errors.New("ffmpeg exited 1"),
		"[out#0/webm @ 0x2] Could not write header (incorrect codec parameters ?): Invalid argument", nil)
	if code, has := bare["code"]; has {
		t.Errorf("a bare header stop carried code %v: want none", code)
	}
}

// A converter the system stopped (out of memory, killed) carries the killed code, so the server can retry
// it as it does a local one; an ordinary exit carries none, as before.
func TestFailureReportCarriesTheKilledCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	killed := exec.Command("sh", "-c", "kill -9 $$").Run()
	body := failureReport(fmt.Errorf("ffmpeg: %w", killed), "", []string{"ffmpeg", "-i", "in.mkv", "out.webm"})
	if body["code"] != jobs.ReasonKilled {
		t.Errorf("code %v, want %s", body["code"], jobs.ReasonKilled)
	}
	exited := exec.Command("sh", "-c", "exit 1").Run()
	if code, has := failureReport(exited, "", nil)["code"]; has {
		t.Errorf("an ordinary exit carried code %v: want none", code)
	}
}
