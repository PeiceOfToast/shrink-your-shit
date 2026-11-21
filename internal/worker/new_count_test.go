// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// newFileCountReads counts the -count_packets reads of the new file: the ones on its temp path.
func newFileCountReads(s *scenario) int {
	n := 0
	for _, c := range s.calls {
		if strings.Contains(c, "-count_packets") && strings.Contains(c, ".syp-tmp") {
			n++
		}
	}
	return n
}

// The verbose report line a whole-file playback test prints, as ffmpeg 7.0.2 writes it (the level marker
// follows the log context).
func reportLine(packets int) string {
	n := strconv.Itoa(packets)
	return "[in#0/matroska,webm @ 0x1] [verbose]   Input stream #0:0 (video): " + n +
		" packets read (1 bytes); " + n + " frames decoded; 0 decode errors;\n"
}

// P1: the playback test reads the whole new file anyway, and its end-of-run report says how many packets it
// read. That is the number the shape check gets, so the file is not read again just to count it.
func TestNewFileCountComesFromThePlaybackRun(t *testing.T) {
	s := newScenario(t)
	s.decodeStderr = reportLine(2500)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q; the count the run reported should be enough", j.Status, j.Error)
	}
	if n := newFileCountReads(s); n != 0 {
		t.Errorf("the new file was read %d more times to count it; calls: %v", n, s.calls)
	}
}

// The number the check uses is the run's, not the fallback's: a report that disagrees with what is expected
// keeps the original, and the file is not read again to count it.
func TestNewFileCountFromThePlaybackRunIsWhatIsChecked(t *testing.T) {
	s := newScenario(t)
	s.decodeStderr = reportLine(1800)
	s.run(Replace)
	s.assertKept("video frames", "check_differs")
	if n := newFileCountReads(s); n != 0 {
		t.Errorf("the new file was read %d more times to count it; calls: %v", n, s.calls)
	}
}

// A long file is played in samples, and a sample reads part of the file: what it read is not the whole
// count, so the count pass stays for that case.
func TestSampledPlaybackDoesNotUseItsOwnCount(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.duration, s.outSpec.duration = 5*3600, 5*3600
	s.decodeStdout = "out_time_us=0\nframe=100\nprogress=end\n" // a sample that played no frames is kept, not passed
	s.decodeStderr = reportLine(100)                            // 100 packets, but only of the sample
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if n := newFileCountReads(s); n != 1 {
		t.Errorf("a sampled playback test has no whole count: the file should be read once to count it, got %d; calls: %v", n, s.calls)
	}
}

// The playback check judges error-level lines only, as it did at -v error: the verbose report and the
// header lines are not decode errors.
func TestErrorLinesKeepsOnlyErrors(t *testing.T) {
	raw := strings.Join([]string{
		"[info] ffmpeg version 7.0.2-static",
		strings.TrimSpace(reportLine(75)),
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
	got := strings.Split(strings.TrimSpace(errorLines([]byte(raw))), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("errorLines kept:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
