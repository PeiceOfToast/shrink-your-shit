// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Review a772397 item 1: the kept-original reason names the max that set the file's bitrate.
func TestKeptVBRWordsNamesTheMaxThatBound(t *testing.T) {
	cases := []struct {
		name string
		v    rules.VideoEncode
		want string
	}{
		{"average cut to the limit", rules.VideoEncode{TargetKbps: 3000, MaxRateKbps: 2400, PeakLimitKbps: 3000},
			"Kept the original: under a 3 Mbps max bitrate the picture would suffer."},
		{"quality mode, the limit is the peak", rules.VideoEncode{MaxRateKbps: 2400, PeakLimitKbps: 3000},
			"Kept the original: under a 3 Mbps max bitrate the picture would suffer."},
		{"the level's figure was under the limit", rules.VideoEncode{TargetKbps: 2000, MaxRateKbps: 2000, PeakLimitKbps: 3000},
			"Kept the original: at a 2 Mbps max bitrate the picture would suffer."},
	}
	for _, c := range cases {
		if got := keptVBRWords(c.v); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Review a772397 item 3: the run log says so when the average is the max too, and says nothing otherwise.
func TestVBRAverageNote(t *testing.T) {
	held := rules.VideoEncode{TargetKbps: 3000, MaxRateKbps: 3000, PeakLimitKbps: 3000}
	if got, want := vbrAverageNote(held), "Average and max bitrate are both 3 Mbps, so the file runs at the max throughout."; got != want {
		t.Errorf("held at the max: got %q, want %q", got, want)
	}
	for name, v := range map[string]rules.VideoEncode{
		"under the limit": {TargetKbps: 2000, MaxRateKbps: 2000, PeakLimitKbps: 3000},
		"no limit":        {TargetKbps: 3000, MaxRateKbps: 3000},
		"quality mode":    {MaxRateKbps: 2400, PeakLimitKbps: 3000},
	} {
		if got := vbrAverageNote(v); got != "" {
			t.Errorf("%s: want no note, got %q", name, got)
		}
	}
}
