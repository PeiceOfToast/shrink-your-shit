// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// FB5b: a 60 fps original converted to 24 fps is compared on the 24 fps grid, so its worst frame 241 is at 10 s,
// not at the 4 s that frame 241 would be on the 60 fps original.
func TestSSIMWorstFrameIsTimedAtTheOutputRate(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.duration, s.outSpec.duration = 20, 20
	s.srcSpec.streams[0]["r_frame_rate"] = "60/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "60/1"
	s.srcPackets, s.outPackets = 1200, 480
	s.encodeStderr = inputReport(1200, 1200, 0)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
	s.ssimStats = "n:241 Y:0.1 U:0.1 V:0.1 All:0.100000 (1.0)\nn:2 Y:0.9 U:0.9 V:0.9 All:0.900000 (1.0)\n"
	maxFPSWorker(s, "24").RunOne(context.Background()) // the kept job's error is not the check; its log is
	lines, _ := s.store.LogLines(s.id)
	var joined []string
	for _, l := range lines {
		if l.Step == 1 && l.Stream == "check" {
			joined = append(joined, l.Text)
		}
	}
	got := strings.Join(joined, "\n")
	if !strings.Contains(got, "Worst frames: 0:00:10 (0.100)") {
		t.Errorf("check lines lack the output-rate time:\n%s", got)
	}
	if j := s.job(); j.Status == jobs.Done {
		t.Errorf("job done; the picture check should have kept the original")
	}
}
