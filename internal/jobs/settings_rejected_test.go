// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

// A10 (user bug): an ffmpeg failure caused by an encoder option is the workflow's settings' fault, not the
// graphics card's. These are the lines real ffmpeg prints (7.0.2, a -global_quality reaching libopus, and a
// -cq no encoder recognizes): the job is not retried on the processor and the card is not blamed.
func TestAnEncoderOptionRefusalIsASettingsError(t *testing.T) {
	cases := [][]string{
		{
			"[libopus @ 0x3c53f740] Quality-based encoding not supported, please specify a bitrate and VBR setting.",
			"[aost#0:1/libopus @ 0x3de1a6c0] Error while opening encoder - maybe incorrect parameters such as bit_rate, rate, width or height.",
			"Conversion failed!",
		},
		{
			"Unrecognized option 'cq'.",
			"Error splitting the argument list: Option not found",
		},
	}
	for _, lines := range cases {
		if got := FailureCode(ReasonConvertFailed, lines); got != "settings_rejected" {
			t.Errorf("%q: code %s, want settings_rejected", strings.Join(lines, " / "), got)
		}
		if FallsBackToProcessor(lines) {
			t.Errorf("%q: falls back to the processor; an option error is the settings' fault", strings.Join(lines, " / "))
		}
	}
}
