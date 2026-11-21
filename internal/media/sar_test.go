// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "testing"

// Only a source ffprobe reports as exactly 1:1 counts as square: an anamorphic SAR and an unknown 0:1 do not.
func TestSquarePixelsReadsTheSampleAspectRatio(t *testing.T) {
	for _, c := range []struct {
		sar  string
		want bool
	}{
		{"1:1", true},
		{"4:3", false},
		{"0:1", false},
		{"", false},
	} {
		m := map[string]any{"codec_type": "video", "width": 1920.0, "height": 1080.0}
		if c.sar != "" {
			m["sample_aspect_ratio"] = c.sar
		}
		if got := parseStream(m, 0).SquarePixels; got != c.want {
			t.Errorf("sample_aspect_ratio %q: SquarePixels %v, want %v", c.sar, got, c.want)
		}
	}
}
