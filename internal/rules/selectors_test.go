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

// Video: a slider position encodes exactly as the Standard selectors at that position's values. But a
// Standard save at those values stays Standard (review 6e60704, P1): Basic's audio and MKV are its own.
func TestSliderIsItsDefaultSelectors(t *testing.T) {
	for q := 1; q <= 5; q++ {
		s := DefaultSelectors(q)
		if s.Validate() != nil {
			t.Fatalf("defaults of %d invalid", q)
		}
		if got := s.Template(); got.Kind != KindSelectors || got.Selectors.Audio != "stereo_aac" || !got.Selectors.OnlyMyLanguages || got.Selectors.FileType != "mkv" { // design E's defaults (S6)
			t.Errorf("position %d at its own values: %+v, want a Standard flow", q, got)
		}
		for _, info := range []media.Info{h264(1920, 1080, 8000), h264(1280, 720, 300), h264(3840, 2160, 40000)} {
			a, b := SliderTemplate(q).Compile(info), Template{Kind: KindSelectors, Selectors: &s, CompilerVersion: 1}.Compile(info)
			a.Kind, b.Kind = "", ""
			if strings.Join(a.FFmpegArgs("i", "o"), " ") != strings.Join(b.FFmpegArgs("i", "o"), " ") || a.Skip != b.Skip {
				t.Errorf("q%d %dx%d: slider %+v vs selectors %+v", q, info.Video().Width, info.Video().Height, a, b)
			}
		}
	}
	s := DefaultSelectors(3)
	s.MaxBitrate = "high"
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
	p = sel(func(s *Selectors) { s.Speed = "slower"; s.MaxBitrate = "high" }).Compile(h264(1920, 1080, 30000))
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if !strings.Contains(args, "-preset slow") || !strings.Contains(args, "-maxrate:v:0 8000k -bufsize:v:0 16000k") { // High, 1080p HEVC (S6 ladder)
		t.Errorf("args %s", args)
	}
	// always standardize: converted however little data it has, by the selectors and the slider alike
	if p := sel(func(s *Selectors) {}).Compile(h264(1920, 1080, 300)); p.Skip != "" {
		t.Errorf("selectors, little data: %q", p.Skip)
	}
	if p := SliderTemplate(3).Compile(h264(1920, 1080, 300)); p.Skip != "" {
		t.Errorf("slider, little data: %q", p.Skip)
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
		{"mp4", media.Stream{Type: "audio", Codec: "truehd"}, ""}, // S6: made E-AC-3 in MP4 (design E)
		{"mp4", media.Stream{Type: "attachment", Codec: "ttf"}, "its attached files (such as fonts) cannot be kept in MP4"},
		{"mkv", media.Stream{Type: "subtitle", Codec: "mov_text"}, ""}, // converted to SRT text inside it (S4)
		{"mp4", media.Stream{Type: "audio", Codec: "aac"}, ""},
		{"mp4", media.Stream{Type: "subtitle", Codec: "mov_text"}, ""},
		{"mkv", media.Stream{Type: "audio", Codec: "truehd"}, ""},
	} {
		p := beside(c.ft).Compile(h264(1920, 1080, 8000, c.s))
		if p.Skip != c.want || (c.want != "" && p.SkipCode != SkipCannotKeepTrack) {
			t.Errorf("%s with %s %s: %q", c.ft, c.s.Type, c.s.Codec, p.Skip)
		}
	}
	// HDR10 is converted and kept as HDR at every flow (S3; hdr_test.go has the rest), with no warning
	hdr := h264(1920, 1080, 8000)
	hdr.Streams[0].ColorTransfer = "smpte2084"
	if p := SliderTemplate(3).Compile(hdr); p.Skip != "" || p.Video.HDR == nil {
		t.Errorf("slider HDR: %q", p.Skip)
	}
	on := sel(func(s *Selectors) { s.IncludeDolbyVision = true; s.FileType = "keep" }) // (MKV, S6's default, warns of renaming)
	if p := on.Compile(hdr); p.Skip != "" || len(on.Warnings()) != 0 {
		t.Errorf("includeDolbyVision on: %q %v", p.Skip, on.Warnings())
	}
	if len(SliderTemplate(3).Warnings()) != 0 {
		t.Error("the slider has no warning")
	}
	// a selectors flow saved with a choice this version cannot run (an older or edited record)
	bad := DefaultSelectors(3)
	bad.Format = "av1"
	if p := (Template{Kind: KindSelectors, Selectors: &bad}).Compile(h264(1920, 1080, 8000)); p.SkipCode != SkipSettingCannotRun {
		t.Errorf("not runnable: %q", p.Skip)
	}
}

func TestSelectorsValidate(t *testing.T) {
	for name, c := range map[string]struct {
		f    func(*Selectors)
		code string
	}{
		"audio unknown":               {func(s *Selectors) { s.Audio = "mp3" }, "invalid"},
		"subtitles unknown":           {func(s *Selectors) { s.Subtitles = "burn_in" }, "invalid"},
		"av1 not yet":                 {func(s *Selectors) { s.Format = "av1" }, "not_available"},
		"unknown speed":               {func(s *Selectors) { s.Speed = "ultrafast" }, "invalid"},
		"unknown cap":                 {func(s *Selectors) { s.MaxBitrate = "15" }, "invalid"},
		"absolute ignore":             {func(s *Selectors) { s.Ignore = []string{"/etc"} }, "invalid"},
		"ignore outside":              {func(s *Selectors) { s.Ignore = []string{"../x"} }, "invalid"},
		"backslash root":              {func(s *Selectors) { s.Ignore = []string{`\foo`} }, "invalid"},
		"drive letter":                {func(s *Selectors) { s.Ignore = []string{"C:"} }, "invalid"},
		"drive path":                  {func(s *Selectors) { s.Ignore = []string{`C:\Videos`} }, "invalid"},
		"drive slash":                 {func(s *Selectors) { s.Ignore = []string{"d:/x"} }, "invalid"},
		"UNC path":                    {func(s *Selectors) { s.Ignore = []string{`\\host\share`} }, "invalid"},
		"dotdot inside":               {func(s *Selectors) { s.Ignore = []string{"a/../../x"} }, "invalid"},
		"dotdot backslash":            {func(s *Selectors) { s.Ignore = []string{`a\..\x`} }, "invalid"},
		"bad pattern":                 {func(s *Selectors) { s.Ignore = []string{"[a"} }, "invalid"},
		"mixed separator bad pattern": {func(s *Selectors) { s.Ignore = []string{`Docs\[`} }, "invalid"}, // canonicalised to Docs/[ before matching
		"quality":                     {func(s *Selectors) { s.Quality = 6 }, "invalid_quality"},
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
	s.Ignore = []string{"Extras", `Shows\Old`, "*.sample.mkv", "a..b", "..hidden"} // dots in a name are fine
	if err := s.Validate(); err != nil {
		t.Errorf("mp4 beside with fine ignore entries: %v", err)
	}
}

func TestIgnored(t *testing.T) {
	s := DefaultSelectors(3)
	s.Ignore = []string{"Extras", "Shows/Old/", "*.sample.mkv", "Season ?? Specials", `Docs\Notes`}
	tmpl := s.Template()
	for rel, want := range map[string]bool{
		"Extras":                    true,
		"Extras/a.mkv":              true,
		"Movie/Extras/b.mkv":        true, // a folder name anywhere
		"Shows/Old/x/e1.mkv":        true,
		"Shows/Older/e1.mkv":        false,
		"Docs/Notes/a.mkv":          true,
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

// The new file's type is checked: the container the flow asked for, or the original's (review 3454c17).
func TestExpectChecksTheContainer(t *testing.T) {
	src := h264(1920, 1080, 8000)
	sel := DefaultSelectors(3)
	sel.AfterConverting = "beside"
	for ft, want := range map[string]string{"mkv": "matroska,webm", "mp4": "mov,mp4,m4a,3gp,3g2,mj2", "keep": "matroska,webm"} {
		sel.FileType = ft
		e := sel.Template().Compile(src).Expect(src, 0)
		if e.Container != want {
			t.Errorf("%s: expects %q", ft, e.Container)
		}
		out := src
		out.Streams = append([]media.Stream(nil), src.Streams...)
		out.Streams[0].Codec = "hevc"
		out.Container = want
		if p := e.Compare(out, 0); len(p) != 0 {
			t.Errorf("%s: the right type refused: %v", ft, p)
		}
		out.Container = "avi"
		if p := e.Compare(out, 0); len(p) == 0 || !strings.Contains(strings.Join(p, ";"), "the new file's type is AVI, not") {
			t.Errorf("%s: a wrong type passed: %v", ft, p)
		}
	}
}

// Replace mode can change the file type (25a-2): it is saved, and its warning (design's words, with the
// type's extensions) says the files are renamed. Keep, or beside, gives no such warning.
func TestReplaceWithNewFileTypeWarnsOfRenaming(t *testing.T) {
	for typ, want := range map[string]string{
		"mkv": "Changing the file type renames each file (movie.mp4 becomes movie.mkv). Radarr, Sonarr, Plex or Jellyfin may lose track of the renamed files: after converting, rescan this library in those apps.",
		"mp4": "Changing the file type renames each file (movie.mkv becomes movie.mp4). Radarr, Sonarr, Plex or Jellyfin may lose track of the renamed files: after converting, rescan this library in those apps.",
	} {
		s := DefaultSelectors(3)
		s.FileType = typ
		if err := s.Validate(); err != nil {
			t.Fatalf("%s when replacing: %v", typ, err)
		}
		if w := s.Template().Warnings(); len(w) != 1 || w[0] != want {
			t.Errorf("%s: warnings %q", typ, w)
		}
		s.AfterConverting = "beside"
		if w := s.Template().Warnings(); len(w) != 0 {
			t.Errorf("%s beside: warnings %q", typ, w)
		}
	}
	keep := DefaultSelectors(3)
	keep.FileType = "keep" // S6's default is MKV, which renames MP4 files (and says so)
	if w := keep.Template().Warnings(); len(w) != 0 {
		t.Errorf("keep: %q", w)
	}
}

// Review 6e60704 (P1): a Standard flow at its default values keeps every audio track and the file type,
// where Basic makes stereo AAC in the person's language and MKV.
func TestDefaultStandardIsNotBasic(t *testing.T) {
	mp4 := h264(1920, 1080, 8000, media.Stream{Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
		media.Stream{Type: "audio", Codec: "aac", Channels: 2, Language: "fre"})
	mp4.Container = "mov,mp4,m4a,3gp,3g2,mj2"
	for i := range mp4.Streams {
		mp4.Streams[i].Index = i
	}
	std := DefaultSelectors(3).Template()
	std.Languages = "eng"
	p := std.Compile(mp4)
	if std.Kind != KindSelectors || p.Skip != "" || p.Container != "mkv" || len(p.Tracks) != 2 { // S6: design E's defaults, still a Standard flow
		t.Errorf("Standard defaults: %s, container %q, tracks %+v", std.Kind, p.Container, p.Tracks)
	}
	keep := DefaultSelectors(3)
	keep.Audio, keep.OnlyMyLanguages, keep.FileType = "as_is", false, "keep" // what an upgraded keep_all flow becomes
	kt := keep.Template()
	kt.Languages = "eng"
	if p := kt.Compile(mp4); p.Container != "" || len(p.Tracks) != 0 {
		t.Errorf("as it is, keep type: container %q, tracks %+v", p.Container, p.Tracks)
	}
	b := BasicTemplate(3, "")
	b.Languages = "eng"
	if p := b.Compile(mp4); p.Container != "mkv" || len(p.Tracks) == 0 {
		t.Errorf("Basic: container %q, tracks %+v", p.Container, p.Tracks)
	}
}

// Control level per flow: each kind's level and tag (design B4), and when a Standard flow fits Basic.
func TestLevelTagAndFitsBasic(t *testing.T) {
	capped := DefaultSelectors(1)
	capped.Resolution, capped.MaxBitrate, capped.Audio = "1080", "medium", "my_languages"
	for _, c := range []struct {
		t          Template
		level, tag string
	}{
		{SliderTemplate(1), "basic", "Basic · Smallest file"},
		{DefaultSelectors(3).Template(), "standard", "Standard · Balanced · same resolution · no bitrate limit · Stereo AAC"},
		{capped.Template(), "standard", "Standard · Smallest file · at most 1080p · Medium bitrate · As it is"},
		{LegacyTemplate(Default(), "replace"), "earlier", "Earlier install"},
		{Template{Kind: KindCustom}, "advanced", "Advanced · Custom"},
	} {
		if c.t.Level() != c.level || c.t.Tag() != c.tag {
			t.Errorf("%s: %s %q, want %s %q", c.t.Kind, c.t.Level(), c.t.Tag(), c.level, c.tag)
		}
	}
	fits := DefaultSelectors(3)
	fits.FileType = "mkv"
	if !fits.FitsBasic() {
		t.Fatal("Basic's own choices")
	}
	for name, f := range map[string]func(*Selectors){
		"slower": func(s *Selectors) { s.Speed = "slower" }, "720": func(s *Selectors) { s.Resolution = "720" },
		"cap": func(s *Selectors) { s.MaxBitrate = "high" }, "subs": func(s *Selectors) { s.Subtitles = "srt" },
		"keep type": func(s *Selectors) { s.FileType = "keep" }, "beside": func(s *Selectors) { s.AfterConverting = "beside" },
		"ignore": func(s *Selectors) { s.Ignore = []string{"Extras"} }, "dv": func(s *Selectors) { s.IncludeDolbyVision = true },
		"aac": func(s *Selectors) { s.Audio = "aac" },
	} {
		s := fits
		f(&s)
		if s.FitsBasic() {
			t.Errorf("%s still fits Basic", name)
		}
	}
	if s := fits; func() bool { s.Audio = "my_languages"; return !s.FitsBasic() }() {
		t.Error("my_languages is Basic's too")
	}
}
