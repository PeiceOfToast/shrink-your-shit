// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
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
	s.Audio, s.OnlyMyLanguages, _ = StandardAudio(audio, true) // 25b's values in S6's terms
	s.FileType = "keep"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	tmpl.Languages = langs
	return tmpl.Compile(info)
}

// Design E's audio choices (S6), on one track set: 5.1 TrueHD (eng), 5.1 AC-3 (eng), stereo AAC (eng),
// stereo MP3 (eng, 128 kbps), 7.1 DTS (eng).
func TestDesignEAudio(t *testing.T) {
	set := withAudio(media.Stream{Codec: "truehd", Channels: 6, Language: "eng"}, media.Stream{Codec: "ac3", Channels: 6, BitrateKbps: 448, Language: "eng"},
		media.Stream{Codec: "aac", Channels: 2, Language: "eng"}, media.Stream{Codec: "mp3", Channels: 2, BitrateKbps: 128, Language: "eng"},
		media.Stream{Codec: "dts", Channels: 8, BitrateKbps: 1536, Language: "eng"})
	describe := func(p Plan) string {
		var out []string
		for _, o := range p.Tracks {
			w := fmt.Sprintf("%d:", o.Index)
			switch {
			case o.Drop:
				w += "drop"
			case o.Add:
				w += "add " + o.Codec + fmt.Sprint(o.Channels)
			default:
				w += o.Codec + fmt.Sprint(o.Channels)
			}
			out = append(out, w)
		}
		return strings.Join(out, " ")
	}
	for choice, want := range map[string]string{
		"stereo_aac":          "1:aac2 2:aac2 5:aac2",             // 3 is stereo AAC, 4 a 128 kbps MP3 (smaller than 160): copied
		"stereo_opus":         "1:opus2 2:opus2 5:opus2",          // 3 (unknown bitrate) and 4 (128 kbps) would not get smaller
		"stereo_aac_surround": "1:add aac2 2:add aac2 5:add aac2", // surround copied, a stereo AAC added from each; 3 and 4 copied
		"eac3_surround":       "1:eac30 5:eac36",                  // lossless and DTS surround (7.1 folded to 5.1); AC-3 and stereo copied
		"ac3_surround":        "1:ac30 5:ac36",
		"as_is":               "",
	} {
		s := DefaultSelectors(3)
		s.Audio, s.FileType = choice, "keep"
		if choice == "stereo_opus" {
			s.FileType = "mkv"
		}
		p := s.Template().Compile(set)
		if got := describe(p); got != want {
			t.Errorf("%s: %q, want %q", choice, got, want)
		}
	}
	// the added stereo track is mapped again after the kept audio, and expected there
	s := DefaultSelectors(3)
	s.Audio, s.FileType = "stereo_aac_surround", "keep"
	p := s.Template().Compile(withAudio(media.Stream{Codec: "ac3", Channels: 6, Language: "fre"}))
	if args := strings.Join(p.FFmpegArgs("i", "o"), " "); !strings.Contains(args, "-map 0:1 -c:a:1 aac -b:a:1 160k -ac:a:1 2") {
		t.Errorf("args %s", args)
	}
	if e := p.Expect(withAudio(media.Stream{Codec: "ac3", Channels: 6, Language: "fre"}), 0); len(e.Streams) != 3 || e.Streams[2].Codec != "aac" || e.Streams[2].Channels != 2 || e.Streams[2].Language != "fre" {
		t.Errorf("expect %+v", e.Streams)
	}
	// MP4 can't hold TrueHD: it becomes E-AC-3 even with "As it is"
	s = DefaultSelectors(3)
	s.Audio, s.FileType, s.AfterConverting = "as_is", "mp4", "beside"
	if p := s.Template().Compile(withAudio(media.Stream{Codec: "truehd", Channels: 6})); describe(p) != "1:eac30" {
		t.Errorf("mp4 truehd: %q", describe(p))
	}
	// the 25b values in S6's terms (design E's migration map)
	for old, want := range map[string]string{"keep_all": "as_is false", "my_languages": "as_is true", "aac": "stereo_aac false", "opus": "stereo_opus false",
		"eac3": "eac3_surround false", "lossless_only": "eac3_surround false", "stereo_aac": "stereo_aac true"} {
		a, only, _ := StandardAudio(old, true)
		if got := fmt.Sprint(a, " ", only); got != want {
			t.Errorf("%s: %s, want %s", old, got, want)
		}
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
	a.Audio, a.FileType, a.AfterConverting = "eac3_surround", "mp4", "beside"
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

// A subtitle of no known kind (TTML: ffmpeg has no decoder for it) stays inside in "Save as separate .srt
// files" and is kept by "Text subtitles only": it is copied, never converted (a conversion would fail).
func TestUnknownKindSubtitleIsCopied(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "subtitle", Codec: "ttml", Language: "eng"},
		media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "eng"})
	for _, choice := range []string{"srt", "text_only"} {
		s := DefaultSelectors(3)
		s.Subtitles = choice
		p := s.Template().Compile(info)
		for _, o := range p.Tracks {
			if o.Index == 1 {
				t.Errorf("%s: the TTML subtitle has an op %+v", choice, o)
			}
		}
		n := 0
		for _, st := range p.Expect(info, 0).Streams {
			if st.Type == "subtitle" {
				n++
			}
		}
		if choice == "srt" && n != 1 || choice == "text_only" && n != 2 {
			t.Errorf("%s: %d subtitles expected in the new file", choice, n)
		}
	}
}
