// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Wording (test matrix item 2): a VBR step names the max the person set, not only the figure it aims at.
func TestVBRStepNamesTheMax(t *testing.T) {
	p := Plan{Video: VideoEncode{CRF: 23, MaxRateKbps: 3200, PeakLimitKbps: 4000, BufsizeKbps: 800}}
	want := ", VBR, aims for about 3200 kbps, never above 4000 kbps."
	if d := videoDecision(p, media.Info{}).Decision; !strings.HasSuffix(d, want) {
		t.Errorf("VBR step %q, want it to end %q", d, want)
	}
}

// Wording (test matrix item 4): codec names as design writes them, in the summary tag.
func TestSummaryTagNamesTheCodec(t *testing.T) {
	s := DefaultSelectors(3)
	s.Format = "h264"
	if tag := s.Template().Tag(); !strings.Contains(tag, " · H.264 · ") {
		t.Errorf("tag %q, want H.264 named", tag)
	}
}
