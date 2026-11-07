// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func h264(w, h, kbps int, extra ...media.Stream) media.Info {
	v := media.Stream{Type: "video", Codec: "h264", Width: w, Height: h, FrameRate: 25, BitrateKbps: kbps, BitDepth: 8}
	return media.Info{Container: "matroska,webm", VideoCodec: "h264", DurationSec: 60, Streams: append([]media.Stream{v}, extra...)}
}

// A slider position compiles exactly as the selectors at that position's values: one recipe.
func TestSliderIsItsDefaultSelectors(t *testing.T) {
	for q := 1; q <= 5; q++ {
		s := DefaultSelectors(q)
		if s.Validate() != nil {
			t.Fatalf("defaults of %d invalid", q)
		}
		if got := s.Template(); got.Kind != KindSlider || got.Params.Quality != q {
			t.Errorf("position %d at its own values: %+v, want the slider", q, got)
		}
		for _, info := range []media.Info{h264(1920, 1080, 8000), h264(1280, 720, 300), h264(3840, 2160, 40000)} {
			a, b := SliderTemplate(q).Compile(info), Template{Kind: KindSelectors, Selectors: &s, CompilerVersion: 1}.Compile(info)
			a.Kind, b.Kind = "", ""
			if strings.Join(a.FFmpegArgs("i", "o"), " ") != strings.Join(b.FFmpegArgs("i", "o"), " ") || a.Skip != b.Skip || a.MinSavingPct != b.MinSavingPct {
				t.Errorf("q%d %dx%d: slider %+v vs selectors %+v", q, info.Video().Width, info.Video().Height, a, b)
			}
		}
	}
	s := DefaultSelectors(3)
	s.MaxBitrate = "10"
	if k, label := s.Template().Label(); k != KindSelectors || label != "Balanced" {
		t.Errorf("label %s %s", k, label)
	}
}

func TestSelectorsCompile(t *testing.T) {
	sel := func(f func(*Selectors)) Template {
		s := DefaultSelectors(3)
		f(&s)
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		return s.Template()
	}
	// resolution: scaled down only; CRF by the new picture size (720p: one lower)
	p := sel(func(s *Selectors) { s.Resolution = "720" }).Compile(h264(1920, 1080, 8000))
	if p.Video.Height != 720 || p.Video.CRF != 22 || !strings.Contains(strings.Join(p.FFmpegArgs("i", "o"), " "), "-filter:v:0 scale=-2:720") {
		t.Errorf("1080 to 720: %+v %v", p.Video, p.FFmpegArgs("i", "o"))
	}
	if e := p.Expect(h264(1920, 1080, 8000), 0); e.Streams[0].Width != 1280 || e.Streams[0].Height != 720 {
		t.Errorf("expect %+v", e.Streams[0])
	}
	if p := sel(func(s *Selectors) { s.Resolution = "1080" }).Compile(h264(1280, 720, 8000)); p.Video.Height != 0 {
		t.Errorf("never made bigger: %+v", p.Video)
	}
	if w := ScaledWidth(1440, 1080, 720); w != 960 {
		t.Errorf("4:3: %d", w)
	}
	if w := ScaledWidth(1920, 800, 480); w != 1152 {
		t.Errorf("2.4:1: %d", w)
	}
	// speed and the cap
	p = sel(func(s *Selectors) { s.Speed = "slower"; s.MaxBitrate = "10" }).Compile(h264(1920, 1080, 30000))
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if !strings.Contains(args, "-preset slow") || !strings.Contains(args, "-maxrate:v:0 10000k -bufsize:v:0 20000k") {
		t.Errorf("args %s", args)
	}
	// skip small off: converted however little data it has
	if p := sel(func(s *Selectors) { s.SkipSmall = false }).Compile(h264(1920, 1080, 300)); p.Skip != "" {
		t.Errorf("skipSmall off: %q", p.Skip)
	}
	if p := SliderTemplate(3).Compile(h264(1920, 1080, 300)); p.SkipCode != SkipAlreadyEfficient {
		t.Errorf("skipSmall on: %q", p.Skip)
	}
	// file type: the new file's, and what it cannot hold
	beside := func(ft string) Template {
		return sel(func(s *Selectors) { s.FileType, s.AfterConverting = ft, "beside" })
	}
	if p := beside("mkv").Compile(h264(1920, 1080, 8000)); p.Container != "mkv" || p.Output != "beside" || p.Skip != "" {
		t.Errorf("mkv: %+v", p)
	}
	for _, c := range []struct {
		ft   string
		s    media.Stream
		want string
	}{
		{"mp4", media.Stream{Type: "subtitle", Codec: "subrip"}, "its subrip subtitles cannot be kept in MP4"},
		{"mp4", media.Stream{Type: "audio", Codec: "truehd"}, "its truehd audio cannot be kept in MP4"},
		{"mp4", media.Stream{Type: "attachment", Codec: "ttf"}, "its attached files (such as fonts) cannot be kept in MP4"},
		{"mkv", media.Stream{Type: "subtitle", Codec: "mov_text"}, "its mov_text subtitles cannot be kept in MKV"},
		{"mp4", media.Stream{Type: "audio", Codec: "aac"}, ""},
		{"mp4", media.Stream{Type: "subtitle", Codec: "mov_text"}, ""},
		{"mkv", media.Stream{Type: "audio", Codec: "truehd"}, ""},
	} {
		p := beside(c.ft).Compile(h264(1920, 1080, 8000, c.s))
		if p.Skip != c.want || (c.want != "" && p.SkipCode != SkipCannotKeepTrack) {
			t.Errorf("%s with %s %s: %q", c.ft, c.s.Type, c.s.Codec, p.Skip)
		}
	}
	// HDR: skipped unless the flow says otherwise, with a warning then
	hdr := h264(1920, 1080, 8000)
	hdr.Streams[0].ColorTransfer = "smpte2084"
	if p := SliderTemplate(3).Compile(hdr); p.SkipCode != SkipHDR {
		t.Errorf("slider HDR: %q", p.Skip)
	}
	off := sel(func(s *Selectors) { s.SkipHDR = false })
	if p := off.Compile(hdr); p.Skip != "" || len(off.Warnings()) != 1 {
		t.Errorf("skipHdr off: %q %v", p.Skip, off.Warnings())
	}
	if len(SliderTemplate(3).Warnings()) != 0 {
		t.Error("the slider has no warning")
	}
	// a selectors flow saved with a choice this version cannot run (an older or edited record)
	bad := DefaultSelectors(3)
	bad.Audio = "aac"
	if p := (Template{Kind: KindSelectors, Selectors: &bad}).Compile(h264(1920, 1080, 8000)); p.SkipCode != SkipSettingCannotRun {
		t.Errorf("not runnable: %q", p.Skip)
	}
}

func TestSelectorsValidate(t *testing.T) {
	for name, c := range map[string]struct {
		f    func(*Selectors)
		code string
	}{
		"audio not yet":           {func(s *Selectors) { s.Audio = "aac" }, "not_available"},
		"subtitles not yet":       {func(s *Selectors) { s.Subtitles = "srt" }, "not_available"},
		"av1 not yet":             {func(s *Selectors) { s.Format = "av1" }, "not_available"},
		"new type when replacing": {func(s *Selectors) { s.FileType = "mkv" }, "not_available"},
		"unknown speed":           {func(s *Selectors) { s.Speed = "ultrafast" }, "invalid"},
		"unknown cap":             {func(s *Selectors) { s.MaxBitrate = "15" }, "invalid"},
		"absolute ignore":         {func(s *Selectors) { s.Ignore = []string{"/etc"} }, "invalid"},
		"ignore outside":          {func(s *Selectors) { s.Ignore = []string{"../x"} }, "invalid"},
		"bad pattern":             {func(s *Selectors) { s.Ignore = []string{"[a"} }, "invalid"},
		"quality":                 {func(s *Selectors) { s.Quality = 6 }, "invalid_quality"},
	} {
		s := DefaultSelectors(3)
		c.f(&s)
		err := s.Validate()
		se, ok := err.(*SelectorError)
		if !ok || se.Code != c.code || !strings.HasSuffix(se.Message, "Nothing was changed.") {
			t.Errorf("%s: %v", name, err)
		}
	}
	s := DefaultSelectors(3)
	s.FileType, s.AfterConverting = "mp4", "beside"
	if err := s.Validate(); err != nil {
		t.Errorf("mp4 beside: %v", err)
	}
}

func TestIgnored(t *testing.T) {
	s := DefaultSelectors(3)
	s.Ignore = []string{"Extras", "Shows/Old/", "*.sample.mkv", "Season ?? Specials"}
	tmpl := s.Template()
	for rel, want := range map[string]bool{
		"Extras":                    true,
		"Extras/a.mkv":              true,
		"Movie/Extras/b.mkv":        true, // a folder name anywhere
		"Shows/Old/x/e1.mkv":        true,
		"Shows/Older/e1.mkv":        false,
		"trailer.sample.mkv":        true,
		"Movie/trailer.sample.mkv":  true,
		"Show/Season 01 Specials/a": true,
		"Movie.mkv":                 false,
		"ExtrasMovie.mkv":           false,
	} {
		if tmpl.Ignored(rel) != want {
			t.Errorf("%s: want %v", rel, want)
		}
	}
	if SliderTemplate(3).Ignored("Extras/a.mkv") {
		t.Error("the slider ignores nothing")
	}
}
