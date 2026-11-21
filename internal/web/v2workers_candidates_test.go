// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// A card with HEVC but no AV1 in the same family: AV1 is not in "Hardware found" and gets one "not tried" line,
// so the job log never says both "found" and "does not support it" for one codec (job 58).
func TestCandidateLinesShowAnUnsupportedCodecOnce(t *testing.T) {
	seamIntelCard(t)
	caps := capabilities(doctor.Tools{
		Hardware:       []string{"hevc_vaapi"},
		HardwareListed: []string{"av1_vaapi"},
		TestedAt:       time.Now(),
	}, true)

	lines := candidateLines(caps, nil, "libsvtav1", false, nil)
	if len(lines) == 0 || lines[0] != "Hardware found: hevc_vaapi." {
		t.Fatalf("hardware found line must list only the supported encoder: %q", lines)
	}
	var av1 []string
	for _, l := range lines {
		if strings.Contains(l, "av1_vaapi") {
			av1 = append(av1, l)
		}
	}
	if len(av1) != 1 || av1[0] != "av1_vaapi: not tried: this graphics card does not support it." {
		t.Errorf("av1_vaapi must have exactly one line, the not-tried one: %q", av1)
	}
}
