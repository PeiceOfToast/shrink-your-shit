// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func withAudio(tracks ...media.Stream) media.Info {
	info := h264(1920, 1080, 8000)
	for i, a := range tracks {
		a.Type, a.Index = "audio", i+1
		info.Streams = append(info.Streams, a)
	}
	return info
}

func compileAudio(t *testing.T, audio string, langs string, info media.Info) Plan {
	t.Helper()
	s := DefaultSelectors(3)
	s.Audio = audio
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	tmpl.Languages = langs
	return tmpl.Compile(info)
}

// Converting never re-encodes a track already in the target codec, never makes one bigger, always
// converts a lossless one, and copies a lossy one whose bitrate is unknown (lead, 13:44Z).
func TestAudioConvertsOnlyToSmaller(t *testing.T) {
	info := withAudio(
		media.Stream{Codec: "aac", Channels: 2, BitrateKbps: 320},        // 1: already AAC: copied, even if big
		media.Stream{Codec: "ac3", Channels: 2, BitrateKbps: 96},         // 2: below the 128k target: copied
		media.Stream{Codec: "ac3", Channels: 6, BitrateKbps: 640},        // 3: above 384k: converted
		media.Stream{Codec: "truehd", Channels: 8},                       // 4: lossless: converted (512k)
		media.Stream{Codec: "dts", Channels: 6},                          // 5: lossy, bitrate unknown: copied
		media.Stream{Codec: "dts", Channels: 6, Profile: "DTS-HD MA"},    // 6: lossless: converted
		media.Stream{Codec: "pcm_s24le", Channels: 2, BitrateKbps: 2304}, // 7: lossless: converted
	)
	p := compileAudio(t, "aac", "", info)
	got := map[int]TrackOp{}
	for _, op := range p.Tracks {
		got[op.Index] = op
	}
	for idx, want := range map[int]int{3: 384, 4: 512, 6: 384, 7: 128} {
		if op, ok := got[idx]; !ok || op.Encoder != "aac" || op.Kbps != want {
			t.Errorf("track %d: %+v, want aac at %dk", idx, op, want)
		}
	}
	for _, idx := range []int{1, 2, 5} {
		if op, ok := got[idx]; ok {
			t.Errorf("track %d must be copied: %+v", idx, op)
		}
	}
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	// a:k counts the new file's audio tracks: tracks 3, 4, 6, 7 are a:2, a:3, a:5, a:6
	for _, want := range []string{"-c:a:2 aac -b:a:2 384k", "-c:a:3 aac -b:a:3 512k", "-c:a:5 aac -b:a:5 384k", "-c:a:6 aac -b:a:6 128k"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	e := p.Expect(info, 0)
	var codecs []string
	for _, s := range e.Streams {
		if s.Type == "audio" {
			codecs = append(codecs, s.Codec)
		}
	}
	if strings.Join(codecs, ",") != "aac,ac3,aac,aac,dts,aac,aac" {
		t.Errorf("expected audio %v", codecs)
	}
	// Opus and E-AC-3 per channel, capped at 640k
	if p := compileAudio(t, "opus", "", withAudio(media.Stream{Codec: "flac", Channels: 6})); len(p.Tracks) != 1 || p.Tracks[0].Encoder != "libopus" || p.Tracks[0].Kbps != 288 {
		t.Errorf("opus %+v", p.Tracks)
	}
	if p := compileAudio(t, "eac3", "", withAudio(media.Stream{Codec: "truehd", Channels: 8})); len(p.Tracks) != 1 || p.Tracks[0].Kbps != 640 {
		t.Errorf("eac3 cap %+v", p.Tracks)
	}
	// lossless only: only the lossless track, to E-AC-3
	p = compileAudio(t, "lossless_only", "", withAudio(media.Stream{Codec: "ac3", Channels: 6, BitrateKbps: 640}, media.Stream{Codec: "truehd", Channels: 6}))
	if len(p.Tracks) != 1 || p.Tracks[0].Index != 2 || p.Tracks[0].Encoder != "eac3" || p.Tracks[0].Kbps != 576 {
		t.Errorf("lossless_only %+v", p.Tracks)
	}
}

// Keep only my languages: other languages are dropped, untagged tracks kept; if none would be left,
// all are kept (no choice drops all audio); with no languages chosen nothing is dropped.
func TestAudioMyLanguages(t *testing.T) {
	info := withAudio(media.Stream{Codec: "aac", Language: "eng"}, media.Stream{Codec: "aac", Language: "deu"},
		media.Stream{Codec: "aac", Language: "jpn"}, media.Stream{Codec: "aac", Language: "und"})
	p := compileAudio(t, "my_languages", "ger,eng", info) // ger and deu are the same language
	if len(p.Tracks) != 1 || !p.Tracks[0].Drop || p.Tracks[0].Index != 3 {
		t.Errorf("drops %+v", p.Tracks)
	}
	if !strings.Contains(strings.Join(p.FFmpegArgs("i", "o"), " "), "-map -0:3") {
		t.Errorf("args %v", p.FFmpegArgs("i", "o"))
	}
	if n := len(p.Expect(info, 0).Streams); n != 1+3 { // video + 3 audio
		t.Errorf("expected %d streams", n)
	}
	if p := compileAudio(t, "my_languages", "fre", withAudio(media.Stream{Codec: "aac", Language: "eng"})); len(p.Tracks) != 0 || !p.AllAudioKept {
		t.Errorf("none in my languages: %+v kept=%v", p.Tracks, p.AllAudioKept)
	}
	if p := compileAudio(t, "my_languages", "", info); len(p.Tracks) != 0 {
		t.Errorf("no languages chosen: %+v", p.Tracks)
	}
}

func TestCanonLang(t *testing.T) {
	for in, want := range map[string]string{"eng": "eng", "EN": "eng", "deu": "ger", "ger": "ger", "de": "ger", "fra": "fre",
		"fre": "fre", "sq": "alb", "sqi": "alb", "und": "", "": "", "xx": "", "zzz": "", "e1g": "", "zxx": "zxx", "chi": "chi", "zho": "chi"} {
		if got := CanonLang(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	// every bibliographic/terminology pair is one language, both ways (review 25b-e336f8c)
	for t2, b := range iso6392T2B {
		if CanonLang(t2) != CanonLang(b) || CanonLang(b) == "" {
			t.Errorf("%s and %s differ", t2, b)
		}
	}
}

// A file tagged "fre" is kept when the setting says "fra", and the other way round.
func TestLanguageFormsMatch(t *testing.T) {
	for _, c := range []struct{ tag, setting string }{{"fre", "fra"}, {"fra", "fre"}, {"ger", "de"}, {"deu", "ger"}, {"alb", "sq"}} {
		p := compileAudio(t, "my_languages", c.setting, withAudio(media.Stream{Codec: "aac", Language: c.tag}, media.Stream{Codec: "aac", Language: "jpn"}))
		if len(p.Tracks) != 1 || p.Tracks[0].Index != 2 {
			t.Errorf("tag %s, setting %s: %+v", c.tag, c.setting, p.Tracks)
		}
	}
}

// Subtitles: my languages keeps those in the list and untagged ones; text only drops picture ones. A
// track left out or converted no longer stops a new file type that could not hold it (slice 25b).
func TestSubtitlePlans(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "eng"},
		media.Stream{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", SubKind: "bitmap", Language: "fre"},
		media.Stream{Index: 3, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "jpn", Forced: true},
		media.Stream{Index: 4, Type: "subtitle", Codec: "ass", SubKind: "text"})
	sel := func(subs, langs string) Plan {
		s := DefaultSelectors(3)
		s.Subtitles = subs
		tmpl := s.Template()
		tmpl.Languages = langs
		return tmpl.Compile(info)
	}
	drops := func(p Plan) (out []int) {
		for _, o := range p.Tracks {
			if o.Drop {
				out = append(out, o.Index)
			}
		}
		return out
	}
	if d := drops(sel("my_languages", "eng,fre")); len(d) != 1 || d[0] != 3 {
		t.Errorf("my languages drops %v, want [3]", d)
	}
	if d := drops(sel("text_only", "")); len(d) != 1 || d[0] != 2 {
		t.Errorf("text only drops %v, want [2]", d)
	}
	p := sel("text_only", "")
	if n := len(p.Expect(info, 0).Streams); n != 1+3 {
		t.Errorf("expected %d streams, want video + 3 subtitles", n)
	}
	if !strings.Contains(strings.Join(p.FFmpegArgs("i", "o"), " "), "-map -0:2") {
		t.Error("the picture subtitle is not unmapped")
	}
	// mp4 cannot hold PGS: text only takes it out, so only subrip (also not for mp4) is the reason now
	mp4 := DefaultSelectors(3)
	mp4.Subtitles, mp4.FileType, mp4.AfterConverting = "text_only", "mp4", "beside"
	if p := mp4.Template().Compile(info); !strings.Contains(p.Skip, "subrip") {
		t.Errorf("mp4 with text only: %q", p.Skip)
	}
	// a TrueHD track converted to E-AC-3 no longer stops an MP4 file
	th := withAudio(media.Stream{Codec: "truehd", Channels: 6})
	a := DefaultSelectors(3)
	a.Audio, a.FileType, a.AfterConverting = "eac3", "mp4", "beside"
	if p := a.Template().Compile(th); p.Skip != "" {
		t.Errorf("truehd to eac3 in mp4: %q", p.Skip)
	}
}

// Subtitles as .srt files: text ones are extracted with names by language (forced and duplicates told
// apart, untagged with no language part); picture ones stay inside.
func TestSubtitlesToSrt(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "en"},
		media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "eng"},
		media.Stream{Index: 3, Type: "subtitle", Codec: "ass", SubKind: "text", Language: "fra", Forced: true},
		media.Stream{Index: 4, Type: "subtitle", Codec: "subrip", SubKind: "text"},
		media.Stream{Index: 5, Type: "subtitle", Codec: "hdmv_pgs_subtitle", SubKind: "bitmap", Language: "ger"})
	s := DefaultSelectors(3)
	s.Subtitles = "srt"
	p := s.Template().Compile(info)
	// native paths on every OS (Windows CI): the staged files and the names beside the new file
	m := filepath.Join(string(filepath.Separator)+"m", "Movie.hevc.mkv")
	var names []string
	for _, o := range p.SubtitleFiles() {
		names = append(names, filepath.Base(SrtName(m, o.Extract)))
		if filepath.Dir(SrtName(m, o.Extract)) != filepath.Dir(m) {
			t.Errorf("%s is not beside the new file", SrtName(m, o.Extract))
		}
	}
	if strings.Join(names, " ") != "Movie.hevc.eng.srt Movie.hevc.eng.2.srt Movie.hevc.fre.forced.srt Movie.hevc.srt" {
		t.Errorf("names %v", names)
	}
	staged := filepath.Join(string(filepath.Separator)+"m", ".Movie.1.syp-tmp", "new.mkv")
	args := strings.Join(p.FFmpegArgs(filepath.Join(string(filepath.Separator)+"m", "Movie.mkv"), staged), " ")
	if want := "-map 0:4 -c:s srt " + filepath.Join(filepath.Dir(staged), "sub.3.srt"); !strings.HasSuffix(args, want) || !strings.Contains(args, "-map -0:1") || strings.Contains(args, "-map -0:5") {
		t.Errorf("args %s", args)
	}
	if n := len(p.Expect(info, 0).Streams); n != 2 { // video and the picture subtitle
		t.Errorf("expected %d streams", n)
	}
}
