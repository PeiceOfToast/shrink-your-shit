// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

// A10 (user bug): an ffmpeg failure caused by an encoder option is the workflow's settings' fault, not the
// graphics card's — but only when an encoder that is there refuses the option. A name ffmpeg does not know means
// this build has no encoder owning it, which is the build's problem and must keep falling back (review #609).
// These are the lines real ffmpeg 7.0.2 prints.
func TestAnEncoderOptionRefusalIsASettingsError(t *testing.T) {
	cases := []struct {
		name         string
		lines        []string
		wantCode     string
		wantFallback bool
	}{
		{"libopus refuses a quality option that reached it", []string{
			"[libopus @ 0x3c53f740] Quality-based encoding not supported, please specify a bitrate and VBR setting.",
			"[aost#0:1/libopus @ 0x3de1a6c0] Error while opening encoder - maybe incorrect parameters such as bit_rate, rate, width or height.",
			"Conversion failed!",
		}, "settings_rejected", false},
		{"the refusal line alone", []string{
			"[libopus @ 0x3c53f740] Quality-based encoding not supported, please specify a bitrate and VBR setting.",
		}, "settings_rejected", false},
		// -cq:v:0 on a build without h264_nvenc (and the same lines on libx264, a planner mismatch): the option's
		// encoder is not there, so this is not the workflow's fault and the processor is tried.
		{"no encoder here owns the option", []string{
			"Unrecognized option 'cq:v:0'.",
			"Error splitting the argument list: Option not found",
		}, "ffmpeg_error", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FailureCode(ReasonConvertFailed, c.lines); got != c.wantCode {
				t.Errorf("%q: code %s, want %s", strings.Join(c.lines, " / "), got, c.wantCode)
			}
			if got := FallsBackToProcessor(c.lines); got != c.wantFallback {
				t.Errorf("%q: falls back %v, want %v", strings.Join(c.lines, " / "), got, c.wantFallback)
			}
		})
	}
}
