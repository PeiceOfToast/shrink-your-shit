// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The per-track wording, pinned line by line on a probed file's facts (built by hand here, so every kind
// of change has a line: a height change, a bitrate cap, a made track, a dropped track, a picture subtitle).
func TestTrackLinesSayTheChange(t *testing.T) {
	video := media.Stream{Index: 0, Type: "video", Codec: "h264", Height: 2160}
	info := media.Info{Streams: []media.Stream{
		video,
		{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 6},
		{Index: 2, Type: "audio", Codec: "aac", Language: "fre", Channels: 2},
		{Index: 3, Type: "audio", Codec: "aac", Language: "spa", Channels: 2},
		{Index: 4, Type: "audio", Codec: "aac", Channels: 2},
		{Index: 5, Type: "subtitle", Codec: "subrip", Language: "eng"},
		{Index: 6, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "ger"},
		{Index: 7, Type: "subtitle", Codec: "subrip", Language: "fre"},
		{Index: 8, Type: "subtitle", Codec: "subrip", Language: "spa"},
	}}
	plan := rules.Plan{
		Video: rules.VideoEncode{Codec: "libx265", Height: 1080, MaxRateKbps: 8000},
		Tracks: []rules.TrackOp{
			{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Channels: 2, Add: true},
			{Index: 2, Type: "audio", Drop: true},
			{Index: 3, Type: "audio", Drop: true},
			{Index: 4, Type: "audio", Encoder: "aac", Codec: "aac", Channels: 2},
			{Index: 7, Type: "subtitle", Drop: true},
			{Index: 8, Type: "subtitle", ToFile: true},
		},
	}

	cases := []struct{ name, got, want string }{
		{"video: height change", trackVideo(plan, info), "Video: H.264 2160p → HEVC 1080p, same length, at most 8 Mbps"},
		{"video: same size", trackVideo(rules.Plan{Video: rules.VideoEncode{Codec: "libx265"}}, info), "Video: H.264 2160p → HEVC, same picture size and length"},
		{"video: repackaged", trackVideo(rules.Plan{Video: rules.VideoEncode{Copy: true}, Container: "mkv"}, media.Info{Streams: []media.Stream{{Index: 0, Type: "video", Codec: "hevc", Height: 1080}}}),
			"Video: HEVC 1080p kept as it is; only repackaged as MKV"},
		{"audio: kept, made, dropped, converted",
			trackAudio(plan, info),
			"Audio: English 5.1 kept, stereo AAC added, track 4 stereo → stereo AAC, French and Spanish dropped"},
		{"subtitles: kept, picture, saved, dropped",
			trackSubtitles(plan, info),
			"Subtitles: English kept, 1 picture subtitle kept, Spanish saved as a separate .srt file, French dropped"},
		{"subtitles: none", trackSubtitles(plan, media.Info{Streams: []media.Stream{video}}), ""},
		{"audio: none", trackAudio(rules.Plan{}, media.Info{Streams: []media.Stream{video}}), "Audio: none kept"},
		{"audio: all kept", trackAudio(rules.Plan{AllAudioKept: true}, info), "Audio: " + rules.NoAudioInMyLanguages},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, c.got, c.want)
		}
	}
}

func TestBitrateCapReadsAsMegabits(t *testing.T) {
	for kbps, want := range map[int]string{8000: "8 Mbps", 2500: "2.5 Mbps", 1000: "1 Mbps"} {
		if got := mbpsWords(kbps); got != want {
			t.Errorf("mbpsWords(%d) = %q, want %q", kbps, got, want)
		}
	}
}

func TestJoinAndNamesTheLastTwo(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"French"}, "French"},
		{[]string{"French", "Spanish"}, "French and Spanish"},
		{[]string{"English", "French", "Spanish"}, "English, French and Spanish"},
	} {
		if got := joinAnd(c.in); got != c.want {
			t.Errorf("joinAnd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
