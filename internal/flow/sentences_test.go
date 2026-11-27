// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func TestSizeSentenceShowsTheSaving(t *testing.T) {
	cases := []struct {
		before, after int64
		want          string
	}{
		{1_200_000_000, 400_000_000, "1.2 GB → 400.0 MB (-67 %)"},
		{1_000_000, 1_000_000, "1.0 MB → 1.0 MB (+0 %)"},
		{0, 500, "0 B → 500 B"},
	}
	for _, c := range cases {
		if got := sizeSentence(c.before, c.after); got != c.want {
			t.Errorf("sizeSentence(%d, %d) = %q, want %q", c.before, c.after, got, c.want)
		}
	}
}

func TestProbeSentenceNamesTheMovieNotTheCoverArt(t *testing.T) {
	info := media.Info{
		Format: media.Format{Duration: 4 * time.Second},
		Video: []media.VideoTrack{
			{Codec: "mjpeg", AttachedPicture: true, Width: 300, Height: 300},
			{Codec: "h264", Width: 1920, Height: 1080},
		},
	}
	if got, want := probeSentence(info), "H.264 1920×1080, 4 s"; got != want {
		t.Errorf("probeSentence = %q, want %q", got, want)
	}
}

func TestStepSentenceIsNumberedAsThePagesCountThem(t *testing.T) {
	if got := stepSentence(2, "Audio"); got != "2. Audio" {
		t.Errorf("stepSentence = %q, want \"2. Audio\"", got)
	}
}
