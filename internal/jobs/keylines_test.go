// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "testing"

// #216 QA: a real x265 failure with only [info] summary lines and exit 255 must
// show no key lines (not the last 5 [info] lines) and the stopped message.
func TestKeyLinesNotInfoAndStoppedMessage(t *testing.T) {
	// The tail of a real x265 encode failure
	info := []string{
		"x265 [info]: HEVC encoder version 3.6",
		"x265 [info]: build info [Linux][GCC 11.2.0][64 bit] 8bit+10bit+12bit",
		"x265 [info]: using cpu capabilities: MMX2 SSE2Fast LZCNT SSSE3 SSE4.2 AVX FMA3 BMI2 AVX2",
		"x265 [info]: Main profile, Level-4 (Main tier)",
		"x265 [info]: frame I:      5, Avg QP:22.01  kb/s: 1123.45",
		"x265 [info]: frame P:    120, Avg QP:23.05  kb/s: 4000.11",
		"x265 [info]: frame B:    945, Avg QP:24.11  kb/s: 5000.22",
		"x265 [info]: encoded 1070 frames in 14.23s (75.20 fps), 4800.00 kb/s, Avg QP: 22.50",
	}
	if got := MatchedKeyLines(info); len(got) != 0 {
		t.Errorf("keyLines should be empty for only [info] lines, got %q", got)
	}
	// The stopped message for exit 255 (with ffmpegRan true)
	if msg, _ := FailureWords("ffmpeg_error", FailureFacts{ExitCode: 255}); msg != "ffmpeg was stopped before it finished (exit code 255)." {
		t.Errorf("255 message: %q", msg)
	}
	// Signal (-1) with ffmpegRan true
	if msg, _ := FailureWords("ffmpeg_error", FailureFacts{ExitCode: -1, FfmpegRan: true}); msg != "ffmpeg was stopped before it finished (stopped by a signal)." {
		t.Errorf("-1 message: %q", msg)
	}
	// Signal (-1) with ffmpegRan false (ffmpeg never started) → old generic message
	if msg, _ := FailureWords("ffmpeg_error", FailureFacts{ExitCode: -1}); msg != "ffmpeg stopped with an error." {
		t.Errorf("-1 no ran: %q", msg)
	}
}

// #216 QA: real error lines are still picked (even with [info] lines before them).
func TestKeyLinesPickRealErrors(t *testing.T) {
	lines := []string{
		"x265 [info]: HEVC encoder version 3.6",
		"x265 [info]: frame I: 5, Avg QP:22.01 kb/s: 1123.45",
		"[h264 @ 0x1] error while decoding MB 12 3",
		"Error opening output files: Conversion failed!",
	}
	got := MatchedKeyLines(lines)
	if len(got) == 0 {
		t.Fatal("real error lines should be picked")
	}
	// The [info] lines must not be among the key lines
	for _, g := range got {
		if InfoLine(g) {
			t.Errorf("key line %q is an info line", g)
		}
	}
}
