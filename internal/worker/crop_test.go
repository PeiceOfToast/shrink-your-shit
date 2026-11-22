// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	r, ok, words, err := w.detectCrop(context.Background(), cropSource(), "in.mkv")
	if err != nil || !ok || r != (rules.CropRect{W: 1920, H: 800, X: 0, Y: 140}) {
		t.Errorf("agreed samples: got %+v ok %v err %v (%s)", r, ok, err, words)
	}
	if x.calls != 10 {
		t.Errorf("samples taken: got %d, want one per spot (10)", x.calls)
	}
}

func TestDetectCropASampleErrorMeansNoCrop(t *testing.T) {
	w := &Worker{Exec: &cropExec{fail: true}, FFmpeg: "ffmpeg"}
	if _, ok, words, _ := w.detectCrop(context.Background(), cropSource(), "in.mkv"); ok || !strings.Contains(words, "not removed") {
		t.Errorf("a failed sample must leave the bars: ok %v, words %q", ok, words)
	}
}

func TestDetectCropDisagreeingSamplesMeanNoCrop(t *testing.T) {
	x := &cropExec{stderr: []string{"crop=1920:800:0:140\n", "crop=1920:806:0:140\n"}}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
	if _, ok, _, _ := w.detectCrop(context.Background(), cropSource(), "in.mkv"); ok {
		t.Error("samples six pixels apart must not be cropped")
	}
}

// A stop during sampling is not a bars failure: detectCrop returns the context's error and the job goes no further.
func TestDetectCropAStopDuringSamplingEndsTheJob(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()
	for _, x := range []*cropExec{
		{stderr: []string{"crop=1920:800:0:140\n"}},
		{fail: true},
	} {
		w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
		_, ok, words, err := w.detectCrop(ctx, cropSource(), "in.mkv")
		if !errors.Is(err, context.Canceled) || ok || words != "" {
			t.Errorf("stopped sampling: ok %v words %q err %v, want the stop as the error and no words", ok, words, err)
		}
	}
}

// blockingExec waits for its context, as a sample does when its time runs out.
type blockingExec struct{ media.Exec }

func (blockingExec) Capture(ctx context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

// The whole detection has a limit: past it, no crop and the job carries on (no error).
func TestDetectCropPastTheTotalLimitIsNoCrop(t *testing.T) {
	old := cropTotalLimit
	cropTotalLimit = 20 * time.Millisecond
	t.Cleanup(func() { cropTotalLimit = old })
	w := &Worker{Exec: blockingExec{}, FFmpeg: "ffmpeg"}
	_, ok, words, err := w.detectCrop(context.Background(), cropSource(), "in.mkv")
	if err != nil || ok || !strings.Contains(words, "took more than") {
		t.Errorf("past the total limit: ok %v err %v words %q, want no crop, no error, the limit in the words", ok, err, words)
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
