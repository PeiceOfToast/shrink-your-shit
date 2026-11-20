// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A 10 s HEVC picture with one 10 s English stereo track, both starting at 0. HEVC is what DefaultExpect expects
// of the main video, so the only thing a test changes is timing.
func timingPicture() media.Info {
	return media.Info{Container: "matroska,webm", DurationSec: 10, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "hevc", Width: 320, Height: 240, StartTime: 0, Duration: 10},
		{Index: 1, Type: "audio", Codec: "aac", Language: "eng", Channels: 2, StartTime: 0, Duration: 10},
	}}
}

// audioTimed is the source with its audio track's timing changed.
func audioTimed(start, dur float64) media.Info {
	out := timingPicture()
	out.Streams = append([]media.Stream(nil), out.Streams...)
	out.Streams[1].StartTime, out.Streams[1].Duration = start, dur
	return out
}

func TestCompareTimingShiftedAudioIsReported(t *testing.T) {
	src := timingPicture()
	p := DefaultExpect(src, 0).Compare(audioTimed(0.5, 10), -1)
	if len(p) != 1 || !strings.Contains(p[0], "out of step") || !strings.Contains(p[0], "audio #1 (eng, 2 channels)") ||
		!strings.Contains(p[0], "0.50 s after the video") {
		t.Fatalf("a track starting 0.5 s late must be reported by name: %v", p)
	}
}

func TestCompareTimingWithinTolerancePasses(t *testing.T) {
	src := timingPicture()
	if p := DefaultExpect(src, 0).Compare(audioTimed(0.05, 10), -1); len(p) != 0 {
		t.Fatalf("0.05 s is within 100 ms: %v", p)
	}
	if p := DefaultExpect(src, 0).Compare(audioTimed(-0.05, 10), -1); len(p) != 0 {
		t.Fatalf("-0.05 s is within 100 ms: %v", p)
	}
}

// A source whose audio already starts 0.3 s late keeps that offset: the check is against the source, not zero.
func TestCompareTimingIsRelativeToTheSource(t *testing.T) {
	src := audioTimed(0.3, 10)
	if p := DefaultExpect(src, 0).Compare(audioTimed(0.3, 10), -1); len(p) != 0 {
		t.Fatalf("the same offset as the source passes: %v", p)
	}
	p := DefaultExpect(src, 0).Compare(audioTimed(0, 10), -1)
	if len(p) != 1 || !strings.Contains(p[0], "in the original it starts 0.30 s after the video") {
		t.Fatalf("losing the source's offset must be reported: %v", p)
	}
}

func TestCompareTimingCutTrackIsReported(t *testing.T) {
	src := timingPicture()
	p := DefaultExpect(src, 0).Compare(audioTimed(0, 8), -1)
	if len(p) != 1 || !strings.Contains(p[0], "audio #1") || !strings.Contains(p[0], "8.0 s long") {
		t.Fatalf("a track 2 s shorter than the picture must be reported: %v", p)
	}
}

// The picture and the source track are both 11 s: the track's 1 s over is the source's length, not cut.
func TestCompareTimingLongerTrackAsInSourceIsAccepted(t *testing.T) {
	src := audioTimed(0, 11)
	out := audioTimed(0, 11)
	out.Streams[0].Duration = 10
	if p := DefaultExpect(src, 0).Compare(out, -1); len(p) != 0 {
		t.Fatalf("11 s audio over a 10 s picture, as in the source, passes: %v", p)
	}
}

func TestCompareTimingNoSourceTimingIsNotChecked(t *testing.T) {
	src := timingPicture()
	e := DefaultExpect(src, 0)
	e.Streams[1].TimingKnown = false // a track made from another has no source timing
	if p := e.Compare(audioTimed(0.5, 3), -1); len(p) != 0 {
		t.Fatalf("a track with no source timing is not checked: %v", p)
	}
}

func TestCompareTimingUnknownLengthIsNotChecked(t *testing.T) {
	src := timingPicture()
	if p := DefaultExpect(src, 0).Compare(audioTimed(0, 0), -1); len(p) != 0 {
		t.Fatalf("a track whose length is not stated is not checked for length: %v", p)
	}
}
