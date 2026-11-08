// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// basic is a Basic flow at Balanced with audio a and the person's languages langs.
func basic(a, langs string) Template {
	t := BasicTemplate(3, a)
	t.Languages = langs
	return t
}

func ops(p Plan) string {
	var out []string
	for _, o := range p.Tracks {
		switch {
		case o.Drop:
			out = append(out, "drop "+itoa(o.Index))
		case o.Encoder != "":
			out = append(out, itoa(o.Index)+" "+o.Encoder+" "+itoa(o.Kbps)+"k "+itoa(o.Channels)+"ch")
		}
	}
	return strings.Join(out, ", ")
}

func itoa(n int) string { return strconv.Itoa(n) }

// Basic's stereo AAC (S4): the tracks in the person's languages and those with no language are kept,
// each as stereo AAC (5.1 downmixed, 160 kbps); one already stereo AAC is copied; other languages
// dropped. With no track in the languages, the main one (default, else first) as stereo AAC, said so.
// With no languages chosen, every track is kept (as stereo AAC): nothing is dropped unasked.
func TestBasicStereoAAC(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
		media.Stream{Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "eng"},
		media.Stream{Index: 3, Type: "audio", Codec: "ac3", Channels: 6, Language: "fre"},
		media.Stream{Index: 4, Type: "audio", Codec: "mp3", Channels: 2})
	p := basic("", "eng").Compile(info)
	if got := ops(p); got != "1 aac 160k 2ch, drop 3, 4 aac 160k 2ch" || p.MainAudioUsed {
		t.Errorf("eng: %s (main %v)", got, p.MainAudioUsed)
	}
	if args := strings.Join(p.FFmpegArgs("i", "o"), " "); !strings.Contains(args, "-c:a:0 aac -b:a:0 160k -ac:a:0 2") || !strings.Contains(args, "-map -0:3") {
		t.Errorf("args %s", args)
	}
	for _, s := range p.Expect(info, 0).Streams {
		if s.Type == "audio" && s.Codec == "aac" && s.Channels != 2 {
			t.Errorf("a converted track is expected stereo: %+v", s)
		}
	}
	// no track in the languages: the main one (here the default), as stereo AAC
	jpn := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "audio", Codec: "ac3", Channels: 6, Language: "jpn"},
		media.Stream{Index: 2, Type: "audio", Codec: "dts", Channels: 6, Language: "ger", Default: true})
	if p := basic("stereo_aac", "eng").Compile(jpn); ops(p) != "drop 1, 2 aac 160k 2ch" || !p.MainAudioUsed {
		t.Errorf("no match: %s (main %v)", ops(p), p.MainAudioUsed)
	}
	// no languages chosen: all kept, each as stereo AAC
	if p := basic("", "").Compile(jpn); ops(p) != "1 aac 160k 2ch, 2 aac 160k 2ch" {
		t.Errorf("no languages: %s", ops(p))
	}
	// the other two choices: 25b's rules; Basic is MKV and replace always
	if p := basic("keep_all", "eng").Compile(info); ops(p) != "" || p.Container != "mkv" || p.Output != "replace" {
		t.Errorf("keep_all: %s %q %q", ops(p), p.Container, p.Output)
	}
	if p := basic("my_languages", "eng").Compile(info); ops(p) != "drop 3" {
		t.Errorf("my_languages: %s", ops(p))
	}
}

// HEVC at Basic (S4): repackaged as MKV with the video copied when anything would change (container,
// audio); skipped as already_in_format when nothing would; AV1 and VP9 stay skipped as efficient.
func TestBasicHEVC(t *testing.T) {
	hevc := func(container string, audio media.Stream) media.Info {
		i := h264(1920, 1080, 4000, audio)
		i.Streams[0].Codec, i.VideoCodec, i.Container = "hevc", "hevc", container
		return i
	}
	stereoEng := media.Stream{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng"}
	mp4 := hevc("mov,mp4,m4a,3gp,3g2,mj2", stereoEng)
	p := basic("", "eng").Compile(mp4)
	args := strings.Join(p.FFmpegArgs("a.mp4", "a.mkv"), " ")
	if p.Skip != "" || !p.Video.Copy || strings.Contains(args, "libx265") || strings.Contains(args, "-tag") || !strings.Contains(args, "-c copy") {
		t.Fatalf("mp4: skip %q args %s", p.Skip, args)
	}
	if e := p.Expect(mp4, 0); !e.VideoCopied || e.Container != Containers["mkv"] || e.Streams[0].Codec != "hevc" {
		t.Errorf("expect %+v", e)
	}
	if p := basic("", "eng").Compile(hevc("matroska,webm", stereoEng)); p.SkipCode != SkipAlreadyInFormat {
		t.Errorf("in format: %q %q", p.SkipCode, p.Skip)
	}
	surround := media.Stream{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"}
	if p := basic("", "eng").Compile(hevc("matroska,webm", surround)); p.Skip != "" || !p.Video.Copy || ops(p) != "1 aac 160k 2ch" {
		t.Errorf("mkv with 5.1: %q %s", p.Skip, ops(p))
	}
	if p := basic("keep_all", "eng").Compile(hevc("matroska,webm", surround)); p.SkipCode != SkipAlreadyInFormat {
		t.Errorf("keep_all in mkv: %q", p.SkipCode)
	}
	av1 := hevc("matroska,webm", stereoEng)
	av1.Streams[0].Codec, av1.VideoCodec = "av1", "av1"
	if p := basic("", "eng").Compile(av1); p.SkipCode != SkipAlreadyHEVC {
		t.Errorf("av1: %q", p.SkipCode)
	}
	// Standard is unchanged: HEVC is skipped there
	if p := standard(func(s *Selectors) {}).Compile(mp4); p.SkipCode != SkipAlreadyHEVC {
		t.Errorf("standard hevc: %q", p.SkipCode)
	}
}

func TestBasicFacts(t *testing.T) {
	if BasicAudio("") != "stereo_aac" || BasicTemplate(2, "keep_all").Params.Audio != "keep_all" ||
		BasicBecomes["video"] != "HEVC" || BasicBecomes["resolution"] != "same" || BasicBecomes["fileType"] != "MKV" {
		t.Error("facts")
	}
	if s, _ := BasicTemplate(2, "").SelectorsView(); s.Audio != "stereo_aac" || s.FileType != "mkv" {
		t.Errorf("view %+v", s)
	}
}
