// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// audioWork is whether the audio step has work for a file with one audio track of this codec and bitrate.
func audioWork(t *testing.T, audioCodec string, bitRate int64) bool {
	t.Helper()
	f := setStep(t, starter(t), "audio.tracks", "codec", audioCodec)
	info := media.Info{
		Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000},
		Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
		Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng", BitRate: bitRate}},
	}
	d, _, err := flow.PlanFile(flow.Revision{WorkflowID: 1, Rev: 1, Flow: f}, info, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range d.Steps {
		if s.ID == "audio" {
			return s.Decision.Work
		}
	}
	t.Fatal("no audio step decision")
	return false
}

// The converts line states the keep rule, and the run agrees with it on both sides: a lossy track at a low bitrate stays
// as it is, and one above the target is converted.
func TestConvertsLineStatesTheAudioKeepRule(t *testing.T) {
	c, _, _ := sentencesOf(t, setStep(t, starter(t), "audio.tracks", "codec", "opus"), 7)
	if !strings.Contains(c, "except tracks already in Opus and lossy tracks already at or below the target bitrate") {
		t.Errorf("converts = %q, want the keep rule stated", c)
	}
	if audioWork(t, "opus", 32_000) {
		t.Error("a 32 kbps AAC track is converted, but the line says lossy tracks at or below the target stay")
	}
	if !audioWork(t, "opus", 512_000) {
		t.Error("a 512 kbps AAC track is kept, but the line says it converts")
	}
}
