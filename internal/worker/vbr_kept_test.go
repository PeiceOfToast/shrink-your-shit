// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Design (SCREENS V3 step 3): a VBR result the picture check refuses keeps the original with words
// that name the figure that actually bound it (review a772397 P3: the level's figure or the peak
// limit); the measured similarity stays in the run log.
func TestVBRKeptWords(t *testing.T) {
	for _, c := range []struct {
		limit int
		words string
	}{
		{3000, "Kept the original: at the level's 1.2 Mbps bitrate the picture would suffer."}, // the level bound it
		{1200, "Kept the original: under a 1.2 Mbps peak limit the picture would suffer."},     // the limit did
	} {
		s := newScenario(t)
		s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.80 U:0.80 V:0.80 All:0.800000 (7.0)"
		w := s.worker(Replace)
		sel := rules.DefaultSelectors(3)
		sel.MaxBitrate, sel.RateControl, sel.VBRMaxKbps, sel.Audio, sel.OnlyMyLanguages = "medium", "vbr", c.limit, "as_is", false
		tmpl := sel.Template()
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Kept || j.Error != c.words {
			t.Errorf("limit %d: %s %q", c.limit, j.Status, j.Error)
		}
		s.assertOriginalSafe()
	}
}
