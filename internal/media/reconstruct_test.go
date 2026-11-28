// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"math"
	"reflect"
	"testing"
	"time"
)

// A summary of a rebuilt probe is the summary it came from: the fields a summary keeps come back as they were. The frame
// rate is held to a millionth, since a summary keeps a float.
func TestTracksInfoRoundTrips(t *testing.T) {
	info := Info{
		Format: Format{Names: []string{"matroska", "webm"}, Duration: 2*time.Hour + 3*time.Second, Size: 4_000_000_000, BitRate: 4_400_000},
		Video: []VideoTrack{
			{Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FrameRate: Rational{Num: 24000, Den: 1001},
				PixelFormat: "yuv420p10le", BitDepth: 10, ColorTransfer: "smpte2084", BitRate: 30_000_000, Language: "und"},
			{Codec: "mjpeg", Width: 300, Height: 300, AttachedPicture: true, Title: "cover"},
		},
		Audio:     []AudioTrack{{Codec: "eac3", Channels: 6, ChannelLayout: "5.1", SampleRate: 48000, Language: "eng", Default: true}},
		Subtitles: []SubtitleTrack{{Codec: "subrip", Language: "eng", Forced: true}},
	}
	want := Summarize(info)
	got := Summarize(want.Info())
	for i := range want.Video {
		if d := math.Abs(want.Video[i].FPS - got.Video[i].FPS); d > 1e-6 {
			t.Errorf("video %d fps = %v, want %v", i, got.Video[i].FPS, want.Video[i].FPS)
		}
		want.Video[i].FPS, got.Video[i].FPS = 0, 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the rebuilt summary differs:\n got %+v\nwant %+v", got, want)
	}
	if rebuilt := want.Info(); rebuilt.Video[0].HDR.DolbyVision || rebuilt.Video[0].ColorTransfer != "smpte2084" {
		t.Errorf("HDR10 was rebuilt as %+v", rebuilt.Video[0])
	}
}
