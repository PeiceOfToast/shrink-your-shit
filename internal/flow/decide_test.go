// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The library preview decides from a summary's rebuilt probe. For the files the starter's Plan is asked about, the decision
// on the rebuilt probe must match the decision on the real one. The cases include files that queue and files that do not,
// so the match is not trivially true.
func TestDecideFileOnARebuiltProbeDecidesTheSame(t *testing.T) {
	rev := flow.Revision{WorkflowID: 7, Rev: 1, Flow: starterFlow(t)}
	probes := []media.Info{
		{ // H.264 1080p 23.976, AAC stereo: the usual file
			Format: media.Format{Names: []string{"mov", "mp4"}, Duration: 2 * time.Hour, Size: 3_000_000_000, BitRate: 3_000_000},
			Video:  []media.VideoTrack{{Codec: "h264", Profile: "High", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 24000, Den: 1001}, PixelFormat: "yuv420p", BitDepth: 8}},
			Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng"}},
		},
		{ // HEVC 2160p HDR10 with 5.1 and subtitles
			Format:    media.Format{Names: []string{"matroska", "webm"}, Duration: 2 * time.Hour, Size: 20_000_000_000, BitRate: 22_000_000},
			Video:     []media.VideoTrack{{Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FrameRate: media.Rational{Num: 24, Den: 1}, PixelFormat: "yuv420p10le", BitDepth: 10, ColorTransfer: "smpte2084"}},
			Audio:     []media.AudioTrack{{Codec: "eac3", Channels: 6, SampleRate: 48000, Language: "eng"}},
			Subtitles: []media.SubtitleTrack{{Codec: "subrip", Language: "eng"}},
		},
		{ // already HEVC 1080p, small
			Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000, BitRate: 2_000_000},
			Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
			Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng"}},
		},
	}
	var queued, left int
	for i, info := range probes {
		want, wantReason := flow.DecideFile(rev, info)
		got, gotReason := flow.DecideFile(rev, media.Summarize(info).Info())
		if got != want || gotReason != wantReason {
			t.Errorf("probe %d: rebuilt decides (%v, %q), the real probe (%v, %q)", i, got, gotReason, want, wantReason)
		}
		if want {
			queued++
		} else {
			left++
		}
	}
	if queued == 0 || left == 0 {
		t.Errorf("the cases should cover both outcomes: %d queue, %d left alone", queued, left)
	}
}
