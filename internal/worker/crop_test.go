// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// cropExec answers each sample with the next stderr text, or fails every sample.
type cropExec struct {
	media.Exec
	stderr []string
	fail   bool
	calls  int
}

func (x *cropExec) Capture(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	if x.fail {
		return nil, nil, errors.New("sample failed")
	}
	s := x.stderr[x.calls%len(x.stderr)]
	x.calls++
	return nil, []byte(s), nil
}

func cropSource() media.Info {
	return media.Info{DurationSec: 100, Streams: []media.Stream{{Index: 0, Type: "video", Width: 1920, Height: 1080}}}
}

func TestDetectCropAgreedSamplesGiveTheRect(t *testing.T) {
	x := &cropExec{stderr: []string{"[Parsed_cropdetect_0 @ 0x1] crop=1920:800:0:140 pts:1\n"}}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
	r, ok, words := w.detectCrop(context.Background(), cropSource(), "in.mkv")
	if !ok || r != (rules.CropRect{W: 1920, H: 800, X: 0, Y: 140}) {
		t.Errorf("agreed samples: got %+v ok %v (%s)", r, ok, words)
	}
	if x.calls != 5 {
		t.Errorf("samples taken: got %d, want one per spot (5)", x.calls)
	}
}

func TestDetectCropASampleErrorMeansNoCrop(t *testing.T) {
	w := &Worker{Exec: &cropExec{fail: true}, FFmpeg: "ffmpeg"}
	if _, ok, words := w.detectCrop(context.Background(), cropSource(), "in.mkv"); ok || !strings.Contains(words, "not removed") {
		t.Errorf("a failed sample must leave the bars: ok %v, words %q", ok, words)
	}
}

func TestDetectCropDisagreeingSamplesMeanNoCrop(t *testing.T) {
	x := &cropExec{stderr: []string{"crop=1920:800:0:140\n", "crop=1920:806:0:140\n"}}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
	if _, ok, _ := w.detectCrop(context.Background(), cropSource(), "in.mkv"); ok {
		t.Error("samples six pixels apart must not be cropped")
	}
}

// The source's picture is cut first, then scaled to the new size: the comparison pairs pictures of one size.
func TestScaleToCutsBeforeItScales(t *testing.T) {
	src := &media.Stream{Width: 1920, Height: 1080}
	crop := &rules.CropRect{W: 1920, H: 800, X: 0, Y: 140}
	cases := []struct {
		name string
		out  *media.Stream
		crop *rules.CropRect
		want string
	}{
		{"no crop, scaled", &media.Stream{Width: 1280, Height: 720}, nil, ",scale=1280:720"},
		{"no crop, same size", &media.Stream{Width: 1920, Height: 1080}, nil, ""},
		{"crop, same as the cut picture", &media.Stream{Width: 1920, Height: 800}, crop, ",crop=1920:800:0:140"},
		{"crop then scale", &media.Stream{Width: 1280, Height: 534}, crop, ",crop=1920:800:0:140,scale=1280:534"},
	}
	for _, c := range cases {
		if got := scaleTo(src, c.out, c.crop); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
