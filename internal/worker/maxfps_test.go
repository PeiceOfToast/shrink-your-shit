// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The picture comparison resamples the original to the new file's rate, so the two streams' frames pair in time.
func TestScaleToResamplesTheOriginalToTheOutputRate(t *testing.T) {
	src := &media.Stream{Width: 1920, Height: 1080}
	same := &media.Stream{Width: 1920, Height: 1080}
	small := &media.Stream{Width: 1280, Height: 720}
	for _, c := range []struct {
		out  *media.Stream
		fps  int
		want string
	}{
		{same, 0, ""},
		{same, 30, ",fps=30"},
		{small, 30, ",scale=1280:720,fps=30"},
	} {
		if got := scaleTo(src, c.out, nil, c.fps); got != c.want {
			t.Errorf("scaleTo(out %dx%d, fps %d) = %q, want %q", c.out.Width, c.out.Height, c.fps, got, c.want)
		}
	}
}
