// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"strings"
	"testing"
)

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
