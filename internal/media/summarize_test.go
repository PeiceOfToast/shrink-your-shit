// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSummarizeFromAProbe(t *testing.T) {
	tracks := Summarize(loadProbe(t, "mp4-h264-aac.json"))

	if tracks.Container != "mov" {
		t.Errorf("container = %q, want mov (the short name)", tracks.Container)
	}
	if tracks.DurationMS != 2000 || tracks.SizeBytes != 222943 {
		t.Errorf("duration/size = %dms/%d, want 2000ms/222943", tracks.DurationMS, tracks.SizeBytes)
	}
	if len(tracks.Video) != 1 || len(tracks.Audio) != 1 {
		t.Fatalf("video/audio = %d/%d, want 1/1", len(tracks.Video), len(tracks.Audio))
	}
	v := tracks.Video[0]
	if v.Codec != "h264" || v.Width != 320 || v.Height != 240 || v.FPS != 30 {
		t.Errorf("video = %+v, want h264 320x240 at 30", v)
	}
	if v.BitDepth != 8 || v.HDR != "SDR" {
		t.Errorf("bit depth/HDR = %d/%s, want 8/SDR", v.BitDepth, v.HDR)
	}
	a := tracks.Audio[0]
	if a.Codec != "aac" || a.Channels != 2 || a.Language != "und" {
		t.Errorf("audio = %+v, want aac stereo, language und", a)
	}
}

func TestSummarizeHDRAndSubtitles(t *testing.T) {
	info := Info{
		Format: Format{Names: []string{"matroska", "webm"}, Duration: 1500 * time.Millisecond},
		Video: []VideoTrack{{
			Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160,
			FrameRate: Rational{Num: 24000, Den: 1001}, PixelFormat: "yuv420p10le", BitDepth: 10,
			ColorTransfer: "smpte2084", HDR: HDR{MasteringDisplay: true, ContentLight: true},
		}},
		Audio:     []AudioTrack{{Codec: "eac3", Channels: 6, ChannelLayout: "5.1", Language: "eng"}},
		Subtitles: []SubtitleTrack{{Codec: "hdmv_pgs_subtitle", Language: "eng", Text: false}},
	}
	tracks := Summarize(info)

	if got := tracks.Video[0].HDR; got != "HDR10" {
		t.Errorf("HDR = %q, want HDR10", got)
	}
	if got := tracks.Video[0].FPS; got < 23.9 || got > 24.0 {
		t.Errorf("FPS = %v, want about 23.976", got)
	}
	if got := tracks.Subtitles[0]; got.Text {
		t.Errorf("PGS subtitle summary = %+v, want text false", got)
	}

	data, err := json.Marshal(tracks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round["durationMs"] != float64(1500) {
		t.Errorf("durationMs = %v, want 1500", round["durationMs"])
	}
}

func TestSummarizeAnEmptyProbe(t *testing.T) {
	tracks := Summarize(Info{})
	if tracks.Container != "" || tracks.DurationMS != 0 || len(tracks.Video) != 0 {
		t.Errorf("summary of nothing = %+v, want zero values", tracks)
	}
}
