// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestOutputSizeIsThePicturesProbeSize: the result check compares the new file's probe with this,
// so it must be the size Build writes: the scale literal when it scales (upright, after ffmpeg's
// autorotation), else the source's own coded size with its rotation left on the stream.
func TestOutputSizeIsThePicturesProbeSize(t *testing.T) {
	cases := []struct {
		name     string
		w, h     int
		rotation int
		box      Scale
		wantW    int
		wantH    int
	}{
		{"no box keeps the coded size", 1920, 1080, 0, Scale{}, 1920, 1080},
		{"a box that does not shrink keeps it", 1920, 1080, 0, Scale{Height: 1080}, 1920, 1080},
		{"a height box fits the shape", 3840, 2160, 0, Scale{Height: 720}, 1280, 720},
		{"a custom box fits inside", 3840, 2160, 0, Scale{Width: 1920, Height: 800}, 1422, 800},
		{"no box keeps a rotated clip's coded size", 320, 240, 90, Scale{}, 320, 240},
		{"a box on a rotated clip is the upright literal", 320, 240, 90, Scale{Height: 180}, 134, 180},
		{"a box on a half-turned clip does not swap", 320, 240, 180, Scale{Height: 180}, 240, 180},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := media.VideoTrack{Width: c.w, Height: c.h, Rotation: c.rotation}
			w, h := OutputSize(v, c.box)
			if w != c.wantW || h != c.wantH {
				t.Errorf("OutputSize = %dx%d, want %dx%d", w, h, c.wantW, c.wantH)
			}
		})
	}
}
