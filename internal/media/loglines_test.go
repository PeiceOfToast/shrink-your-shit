// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"strings"
	"testing"
)

// x265 writes its own log straight to stderr, without ffmpeg's level marker: its info lines (the banner, CPU
// caps, thread pool, the frame summary) are not errors and stay out of the job log, while its warning and
// error lines stay (#588 QA case 2: 23 [info] lines buried the lines that matter).
func TestErrorLinesDropsX265InfoLines(t *testing.T) {
	raw := strings.Join([]string{
		"x265 [info]: HEVC encoder version 4.1",
		"x265 [info]: build info [Linux][GCC 11.2.0][64 bit] 8bit+10bit+12bit",
		"x265 [info]: using cpu capabilities: MMX2 SSE2Fast LZCNT SSSE3 SSE4.2 AVX FMA3 BMI2 AVX2",
		"x265 [info]: frame I:      5, Avg QP:22.01  kb/s: 1123.45",
		"x265 [warning]: halving the quality when psy-rd is enabled for 444 input",
		"x265 [error]: unknown option: bogus",
	}, "\n")
	want := "x265 [warning]: halving the quality when psy-rd is enabled for 444 input\n" +
		"x265 [error]: unknown option: bogus"
	if got := ErrorLines([]byte(raw)); got != want {
		t.Errorf("ErrorLines kept:\n%s\nwant:\n%s", got, want)
	}
}

// The checks judge error-level lines only, as they did at -v error: the verbose report and the header lines
// are not decode errors.
func TestErrorLinesKeepsOnlyErrors(t *testing.T) {
	raw := strings.Join([]string{
		"[info] ffmpeg version 7.0.2-static",
		"[in#0/matroska,webm @ 0x1] [verbose]   Input stream #0:0 (video): 75 packets read (1 bytes); 75 frames decoded; 0 decode errors;",
		"[matroska,webm @ 0x5] [warning] some warning",
		"[h264 @ 0x2] [error] error while decoding MB 3 11",
		"[vist#0:0/h264 @ 0x3] [dec:h264 @ 0x4] [fatal] corrupt decoded frame",
		"[error] a plain error line",
		"[aac @ 0x6] Error while decoding stream #0:1: Invalid data found when processing input",
	}, "\n")
	want := []string{
		"[h264 @ 0x2] error while decoding MB 3 11",
		"[vist#0:0/h264 @ 0x3] [dec:h264 @ 0x4] corrupt decoded frame",
		"a plain error line",
		"[aac @ 0x6] Error while decoding stream #0:1: Invalid data found when processing input",
	}
	got := strings.Split(strings.TrimSpace(ErrorLines([]byte(raw))), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ErrorLines kept:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
