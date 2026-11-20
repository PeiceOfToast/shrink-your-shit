// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// ssimOutStart reads the start time the picture comparison gives the new file's video (the setpts of
// its [1:v:0] input), which is where the alignment try shows.
func ssimOutStart(t *testing.T, args []string) float64 {
	t.Helper()
	var filter string
	for i, a := range args {
		if a == "-lavfi" && i+1 < len(args) {
			filter = args[i+1]
		}
	}
	const pre = "[1:v:0]setpts=PTS-("
	i := strings.Index(filter, pre)
	if i < 0 {
		t.Fatalf("the picture comparison does not start the new file's video: %q", filter)
	}
	rest := filter[i+len(pre):]
	j := strings.Index(rest, ")/TB")
	if j < 0 {
		t.Fatalf("the picture comparison's start time is unreadable: %q", filter)
	}
	at, err := strconv.ParseFloat(rest[:j], 64)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// P3: the picture comparison finds the frame alignment once. Three places are compared; a file whose
// pictures already line up takes one run each (3). A file one frame out takes two on the first place
// and one on each of the others (4), because the alignment that passed is tried first from then on.
func TestPictureComparisonFindsTheAlignmentOnce(t *testing.T) {
	const good = "[Parsed_ssim_0] SSIM Y:0.99 U:0.99 V:0.99 All:0.990000 (20.0)"
	const bad = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
	frame := 0.04 // the fixtures' 25 fps
	for _, c := range []struct {
		name    string
		aligned float64
		starts  []float64
	}{
		{"already aligned", 0, []float64{0, 0, 0}},
		{"one frame early", -frame, []float64{0, -frame, -frame, -frame}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			var starts []float64
			s.ssim = func(args []string) (string, string, error) {
				at := ssimOutStart(t, args)
				starts = append(starts, at)
				if math.Abs(at-c.aligned) < 1e-9 {
					return "", good, nil
				}
				return "", bad, nil
			}
			s.run(Replace)
			if j := s.job(); j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			if len(starts) != len(c.starts) {
				t.Fatalf("the picture comparison ran %d times at %v, want %d (%v)", len(starts), starts, len(c.starts), c.starts)
			}
			for i := range c.starts {
				if math.Abs(starts[i]-c.starts[i]) > 1e-9 {
					t.Fatalf("run %d compared at %v, want %v (all: %v)", i+1, starts[i], c.starts[i], starts)
				}
			}
		})
	}
}
