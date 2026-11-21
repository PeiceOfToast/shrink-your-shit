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

// Wording (slice 16): a level step names the bitrate it aims at and its peak, not the CRF it does not use.
func TestLevelStepNamesTheBitrate(t *testing.T) {
	p := Plan{Video: VideoEncode{CRF: 23, TargetKbps: 1800, MaxRateKbps: 2700}}
	d := videoDecision(p, media.Info{}).Decision
	if want := "at about 1800 kbps, peaks up to 2700 kbps."; !strings.HasSuffix(d, want) {
		t.Errorf("level step %q, want it to end %q", d, want)
	}
	if strings.Contains(d, "CRF") {
		t.Errorf("level step %q says CRF, but the encode runs -b:v", d)
	}
}

// Wording (slice 16): a quality step still says the CRF it runs.
func TestQualityStepNamesTheCRF(t *testing.T) {
	p := Plan{Video: VideoEncode{CRF: 23}}
	if d := videoDecision(p, media.Info{}).Decision; !strings.Contains(d, "at CRF 23.") {
		t.Errorf("quality step %q, want it to say at CRF 23", d)
	}
}

// Wording (slice 16, review 581): a quality step with a cap says the cap, after the CRF it runs.
func TestQualityCapStepNamesTheCap(t *testing.T) {
	p := Plan{Video: VideoEncode{CRF: 23, MaxRateKbps: 6000}}
	if want := ", and never above 6000 kbps."; !strings.HasSuffix(videoDecision(p, media.Info{}).Decision, want) {
		t.Errorf("capped quality step %q, want it to end %q", videoDecision(p, media.Info{}).Decision, want)
	}
}
