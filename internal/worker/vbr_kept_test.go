// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Design (SCREENS V3 step 3): a VBR result the picture check refuses keeps the original with the
// design's words, naming the person's peak limit; the measured similarity stays in the run log.
func TestVBRKeptWords(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.80 U:0.80 V:0.80 All:0.800000 (7.0)"
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.MaxBitrate, sel.RateControl, sel.VBRMaxKbps, sel.Audio, sel.OnlyMyLanguages = "medium", "vbr", 3000, "as_is", false
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Kept || j.Error != "Kept the original: under a 3 Mbps peak limit the picture would suffer." {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}
