// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// maxFPSWorker converts on the processor with the frame-rate limit the Convert step would set.
func maxFPSWorker(s *scenario, maxFrameRate string) *Worker {
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.Hardware = "cpu"
	sel.MaxFrameRate = maxFrameRate
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	return w
}

// FB5b: a 60 fps original converted to at most 30 fps has its report's 120 decoded frames at the output rate, so
// the new file is checked against 60 frames. A file with 60 frames passes; one with the source's 120 does not.
func TestCappedPlanChecksTheNewFileAtTheOutputRate(t *testing.T) {
	for _, c := range []struct {
		name       string
		outPackets int
		done       bool
	}{
		{"the new file has the frames at 30 fps", 60, true},
		{"the new file keeps the source's 120 frames", 120, false},
	} {
		s := newScenario(t)
		s.srcSpec.duration, s.outSpec.duration = 2, 2
		s.srcSpec.streams[0]["r_frame_rate"] = "60/1"
		s.srcSpec.streams[0]["avg_frame_rate"] = "60/1"
		s.srcPackets, s.outPackets = 120, c.outPackets
		s.encodeStderr = inputReport(120, 120, 0)
		maxFPSWorker(s, "30").RunOne(context.Background()) // a kept or failed job returns its error; the status is the check
		if j := s.job(); (j.Status == jobs.Done) != c.done {
			t.Errorf("%s: status %s (error %q), want done=%v", c.name, j.Status, j.Error, c.done)
		}
	}
}

// The count the new file is checked against, with and without a limit, for each kind of original.
func TestCheckedFramesAtTheOutputRate(t *testing.T) {
	steady := media.StreamStats{Packets: 120, Frames: 120}
	for _, c := range []struct {
		name    string
		guess   int64
		st      media.StreamStats
		have    bool
		vfr     bool
		maxFPS  int
		srcRate float64
		want    int64
	}{
		{"no limit: the report's count, as today", 120, steady, true, false, 0, 60, 120},
		{"limit 30 on 60 fps: the count at 30", 60, steady, true, false, 30, 60, 60},
		{"limit 24 on 25 fps: rounded to the output rate", 50, media.StreamStats{Frames: 50}, true, false, 24, 25, 48},
		{"limit with a VFR original: skipped", 60, steady, true, true, 30, 60, 0},
		{"limit with an unknown source rate: skipped", 60, steady, true, false, 30, 0, 0},
		{"limit, no report: the estimate already at the output rate", 60, media.StreamStats{}, false, false, 30, 60, 60},
	} {
		if got := checkedFrames(c.guess, c.st, c.have, c.vfr, c.maxFPS, c.srcRate); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
