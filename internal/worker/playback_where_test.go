// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"strings"
	"testing"
)

// The job log's Where line gives playback's position from ffmpeg's frame count over the frame rate, because ffmpeg's
// own out_time is 0 when it decodes to the null muxer. A sample adds its start time.
func TestPlaybackWhereSaysFrameAndTime(t *testing.T) {
	cases := []struct {
		name          string
		progress      string
		start, length float64
		fps           float64
		want          string
	}{
		{"whole file, rate known", "out_time_us=0\nframe=209\nprogress=end\n", 0, 0, 25,
			"Where: playback reached frame 209 (about 0:00:08)"},
		{"whole file, rate unknown", "out_time_us=0\nframe=209\nprogress=end\n", 0, 0, 0,
			"Where: playback reached frame 209"},
		{"sample from 2 s", "out_time_us=0\nframe=50\nprogress=end\n", 2, 5, 25,
			"Where: playback reached frame 50 (about 0:00:04)"},
		{"no frame reported", "progress=end\n", 0, 0, 25,
			"Where: playback did not report how far it got"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := playbackKept([]byte(c.progress), "[hevc @ 0x1] Invalid NAL unit size 5", c.start, c.length, 10, c.fps)
			var k *KeptError
			if !errors.As(err, &k) {
				t.Fatalf("playbackKept returned %v, want a *KeptError", err)
			}
			for _, l := range k.Detail {
				if strings.HasPrefix(l, "Where: ") {
					if l != c.want {
						t.Errorf("Where line %q, want %q", l, c.want)
					}
					return
				}
			}
			t.Errorf("no Where line in detail %q", k.Detail)
		})
	}
}
