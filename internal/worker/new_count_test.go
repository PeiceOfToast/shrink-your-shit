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
