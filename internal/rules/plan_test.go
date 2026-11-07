// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// todayArgs are the ffmpeg arguments the program ran before Plans (media.HEVCArgs, removed): a
// legacy flow must keep producing exactly these.
func todayArgs(in, out, crf, preset string) string {
	return "-y -nostdin -v error -progress pipe:1 -nostats -i " + in +
		" -map 0 -map -0:d -c copy -c:v:0 libx265 -crf " + crf + " -preset " + preset + " -tag:v:0 hvc1 " + out
}

// probeSet is every probe in internal/media/testdata plus synthetic files around the default rule's
// edges (codec, bitrate).
func probeSet(t *testing.T) map[string]media.Info {
	t.Helper()
	set := map[string]media.Info{}
	files, _ := filepath.Glob(filepath.Join("..", "media", "testdata", "*", "*.json"))
	if len(files) < 15 {
		t.Fatalf("expected the media test probes, found %d", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		info, err := media.ParseProbe(b)
		if err != nil { // e.g. no video: the scanner records it as unreadable before any Plan
			continue
		}
		set[filepath.Base(filepath.Dir(f))+"/"+filepath.Base(f)] = info
	}
	for name, i := range map[string]media.Info{
		"h264 at 3999 kbps": {VideoCodec: "h264", BitrateKbps: 3999},
		"h264 at 4000 kbps": {VideoCodec: "h264", BitrateKbps: 4000},
		"h264 at 9000 kbps": {VideoCodec: "h264", BitrateKbps: 9000},
		"hevc at 9000 kbps": {VideoCodec: "hevc", BitrateKbps: 9000},
		"mpeg2 at 9000":     {VideoCodec: "mpeg2video", BitrateKbps: 9000},
		"no video":          {},
	} {
		set[name] = i
	}
	return set
}

// The legacy regression (D13): an upgraded install's flow converts exactly as before Plans, with the
// same decisions, reasons and ffmpeg arguments, for every probe. One change on purpose (slice 25a, D3):
// an HDR or Dolby Vision file the rule would convert is now skipped as hdr_dv.
func TestLegacyPlanIsExactlyTodaysBehaviour(t *testing.T) {
	for _, r := range []Rule{Default(), {FromCodec: "h264", MinBitrateKbps: 3000, CRF: 24, Preset: "fast"}, {FromCodec: "mpeg2video", MinBitrateKbps: 0, CRF: 30, Preset: "ultrafast"}} {
		for _, out := range []string{"replace", "beside"} {
			tmpl := LegacyTemplate(r, out)
			for name, info := range probeSet(t) {
				p := tmpl.Compile(info)
				ok, why := r.Match(info)
				if ok && info.IsHDR() {
					if p.SkipCode != SkipHDR {
						t.Errorf("%s %+v: an HDR file the rule converts must be skipped as hdr_dv, got %q", name, r, p.Skip)
					}
					continue
				}
				if ok != (p.Skip == "") || (ok && p.Reason != why) || (!ok && p.Skip != why) {
					t.Errorf("%s %+v: plan skip %q reason %q; today match=%v %q", name, r, p.Skip, p.Reason, ok, why)
				}
				got := strings.Join(p.FFmpegArgs("/in dir/a.mkv", "/in dir/.a.1.syp-tmp/new.mkv"), " ")
				want := todayArgs("/in dir/a.mkv", "/in dir/.a.1.syp-tmp/new.mkv", strconv.Itoa(r.CRF), r.Preset)
				if got != want {
					t.Fatalf("%s: args\n got %s\nwant %s", name, got, want)
				}
				if p.Output != out || p.MinSSIM != 0 {
					t.Errorf("%s: output %q, threshold %v: a legacy flow keeps the program's defaults", name, p.Output, p.MinSSIM)
				}
				if e := p.Expect(info, 100); e.MinSSIM != 0 {
					t.Errorf("%s: expect %+v", name, e)
				}
			}
		}
	}
}

// probe is a file with one video stream (and, optionally, more streams after it).
func probe(codec string, w, h int, fps float64, kbps int, more ...media.Stream) media.Info {
	return media.Info{Container: "matroska,webm", VideoCodec: codec, BitrateKbps: kbps, DurationSec: 60,
		Streams: append([]media.Stream{{Type: "video", Codec: codec, Width: w, Height: h, FrameRate: fps}}, more...)}
}

func TestSliderPlanUsesTheTable(t *testing.T) {
	info := probe("h264", 1920, 1080, 24, 10000) // 0.201 bits per pixel: every position converts it
	for _, pos := range Slider {
		p := SliderTemplate(pos.Position).Compile(info)
		if p.Skip != "" || p.Video.CRF != pos.CRF || p.Video.Preset != "medium" ||
			p.MinSSIM != pos.MinSSIM || p.Output != "replace" || !strings.Contains(p.Reason, "H.264 at 10000 kbps") {
			t.Errorf("position %d: %+v", pos.Position, p)
		}
		e := p.Expect(info, 0)
		if e.MinSSIM != pos.MinSSIM {
			t.Errorf("position %d: expect %+v", pos.Position, e)
		}
	}
	if p := SliderTemplate(9).Compile(info); p.Skip == "" || p.Video.Codec != "" {
		t.Errorf("an unknown position cannot convert: %+v", p)
	}
	if p := (Template{Kind: KindCustom}).Compile(info); p.Skip == "" || p.Video.Codec != "" {
		t.Errorf("a custom flow cannot run yet: %+v", p)
	}
}

func TestSliderCRFFollowsTheResolution(t *testing.T) {
	for _, c := range []struct {
		w, h, crf int
	}{{720, 480, 22}, {1280, 720, 22}, {1920, 1080, 23}, {2560, 1440, 23}, {3840, 2160, 24}, {3840, 1600, 24}} {
		p := SliderTemplate(3).Compile(probe("h264", c.w, c.h, 24, 80000))
		if p.Video.CRF != c.crf {
			t.Errorf("%dx%d at Balanced: crf %d, want %d", c.w, c.h, p.Video.CRF, c.crf)
		}
	}
}

func TestSliderConvertsOnlyOlderFormats(t *testing.T) {
	for codec, wantSkip := range map[string]string{
		"hevc": "HEVC is already an efficient format", "av1": "AV1 is already an efficient format",
		"vp9": "VP9 is already an efficient format", "prores": "prores is not a format this setting converts",
		"h264": "", "mpeg2video": "", "mpeg4": "", "vc1": "", "wmv3": "",
	} {
		if p := SliderTemplate(3).Compile(probe(codec, 1920, 1080, 25, 20000)); p.Skip != wantSkip {
			t.Errorf("%s: skip %q, want %q", codec, p.Skip, wantSkip)
		}
	}
}

// Eligibility is bits per pixel per frame above the position's threshold (it replaces the fixed bitrate).
func TestSliderSkipsFilesThatAreAlreadySmall(t *testing.T) {
	px := 1920.0 * 1080 * 24
	for _, pos := range Slider {
		above := int(pos.MinBPP*px/1000) + 50
		below := int(pos.MinBPP*px/1000) - 50
		if p := SliderTemplate(pos.Position).Compile(probe("h264", 1920, 1080, 24, above)); p.Skip != "" {
			t.Errorf("%s at %d kbps: %q", pos.Name, above, p.Skip)
		}
		p := SliderTemplate(pos.Position).Compile(probe("h264", 1920, 1080, 24, below))
		if !strings.HasPrefix(p.Skip, "already efficient") || p.Video.Codec == "" {
			t.Errorf("%s at %d kbps: skip %q (the Plan still says how it would convert)", pos.Name, below, p.Skip)
		}
	}
	if p := SliderTemplate(3).Compile(probe("h264", 1920, 1080, 0, 9000)); !strings.Contains(p.Skip, "unknown") {
		t.Errorf("no frame rate: %q", p.Skip)
	}
}

// The source's bit depth is kept: nothing forces a pixel format, and the result must match it.
func TestSliderKeepsTenBit(t *testing.T) {
	info := probe("h264", 1920, 1080, 24, 12000)
	info.Streams[0].BitDepth = 10
	p := SliderTemplate(3).Compile(info)
	args := strings.Join(p.FFmpegArgs("a.mkv", "b.mkv"), " ")
	if p.Skip != "" || strings.Contains(args, "pix_fmt") || p.Expect(info, 0).Streams[0].BitDepth != 10 {
		t.Errorf("skip %q args %s", p.Skip, args)
	}
}

// Cover art stored as the first video stream: the film is encoded, the picture copied as it is.
func TestSliderEncodesTheMainVideoNotCoverArt(t *testing.T) {
	info := probe("mjpeg", 600, 600, 0, 12000)
	info.Streams[0].AttachedPic = true
	info.Streams = append(info.Streams, media.Stream{Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 24, BitrateKbps: 12000})
	p := SliderTemplate(3).Compile(info)
	args := strings.Join(p.FFmpegArgs("a.mp4", "b.mp4"), " ")
	if p.Skip != "" || !strings.Contains(args, "-c:v:1 libx265") || !strings.Contains(args, "-tag:v:1 hvc1") {
		t.Fatalf("skip %q args %s", p.Skip, args)
	}
	e := p.Expect(info, 0)
	if e.Streams[0].Codec != "mjpeg" || e.Streams[1].Codec != "hevc" {
		t.Errorf("expect %+v", e.Streams)
	}
}

func TestSliderSkipsSubtitlesTheContainerCannotHold(t *testing.T) {
	info := probe("h264", 1920, 1080, 24, 12000, media.Stream{Type: "subtitle", Codec: "hdmv_pgs_subtitle"})
	if p := SliderTemplate(3).Compile(info); p.Skip != "" {
		t.Errorf("mkv holds any subtitle: %q", p.Skip)
	}
	info.Container = "mp4"
	if p := SliderTemplate(3).Compile(info); !strings.Contains(p.Skip, "hdmv_pgs_subtitle subtitles cannot be kept in mp4") {
		t.Errorf("mp4: %q", p.Skip)
	}
	info.Streams[1].Codec = "mov_text"
	if p := SliderTemplate(3).Compile(info); p.Skip != "" {
		t.Errorf("mp4 with mov_text: %q", p.Skip)
	}
}

// The fingerprint names the settings that decide and judge a conversion, not the file.
func TestSnapshotFingerprint(t *testing.T) {
	info := media.Info{VideoCodec: "h264", BitrateKbps: 8000, DurationSec: 60, Streams: []media.Stream{{Type: "video", Codec: "h264"}}}
	other := media.Info{VideoCodec: "h264", BitrateKbps: 12000, DurationSec: 90, Streams: []media.Stream{{Type: "video", Codec: "h264"}, {Type: "audio", Codec: "aac"}}}
	verify := Verification{MinSSIM: 0.95, DecodeCheck: "auto", FullDecodeMaxSec: 10800}
	snap := func(t Template, i media.Info, v Verification) Snapshot {
		p := t.Compile(i)
		return Snapshot{Plan: p, Expect: p.Expect(i, 100), Verify: v}
	}
	base := LegacyTemplate(Default(), "replace")
	a := snap(base, info, verify).Fingerprint()
	if b := snap(base, other, verify).Fingerprint(); a != b || len(a) != 16 {
		t.Errorf("the same settings on two files: %s %s", a, b)
	}
	// two legacy flows with the same encoder but another eligibility rule must differ
	for name, r := range map[string]Rule{
		"other source codec": {FromCodec: "mpeg2video", MinBitrateKbps: 4000, CRF: 26, Preset: "medium"},
		"other bitrate":      {FromCodec: "h264", MinBitrateKbps: 6000, CRF: 26, Preset: "medium"},
		"other crf":          {FromCodec: "h264", MinBitrateKbps: 4000, CRF: 24, Preset: "medium"},
	} {
		if snap(LegacyTemplate(r, "replace"), info, verify).Fingerprint() == a {
			t.Errorf("%s: same fingerprint", name)
		}
	}
	if snap(LegacyTemplate(Default(), "beside"), info, verify).Fingerprint() == a {
		t.Error("output mode: same fingerprint")
	}
	for name, v := range map[string]Verification{
		"decode check always": {MinSSIM: 0.95, DecodeCheck: "always", FullDecodeMaxSec: 10800},
		"another SSIM":        {MinSSIM: 0.97, DecodeCheck: "auto", FullDecodeMaxSec: 10800},
	} {
		if snap(base, info, v).Fingerprint() == a {
			t.Errorf("%s: same fingerprint", name)
		}
	}
}

// A bitrate cap (the Standard selectors' maxBitrate, slice 25) only adds its own arguments.
func TestPlanCarriesABitrateCap(t *testing.T) {
	p := LegacyTemplate(Default(), "replace").Compile(media.Info{VideoCodec: "h264", BitrateKbps: 8000})
	p.Video.MaxRateKbps = 6000
	got := strings.Join(p.FFmpegArgs("a.mkv", "b.mkv"), " ")
	if !strings.Contains(got, "-crf 26 -preset medium -maxrate:v:0 6000k -bufsize:v:0 12000k -tag:v:0 hvc1 b.mkv") {
		t.Errorf("%s", got)
	}
}

// A slider position's eligibility (bits per pixel) is part of what its snapshot records and fingerprints.
func TestSliderEligibilityIsInTheFingerprint(t *testing.T) {
	info := probe("h264", 1920, 1080, 24, 12000)
	p := SliderTemplate(3).Compile(info)
	if p.Eligibility.MinBitsPerPixel != 0.08 {
		t.Errorf("%+v", p.Eligibility)
	}
	v := Verification{MinSSIM: p.MinSSIM, DecodeCheck: "auto", FullDecodeMaxSec: 10800}
	a := Snapshot{Plan: p, Expect: p.Expect(info, 0), Verify: v}.Fingerprint()
	p.Eligibility.MinBitsPerPixel = 0.09
	if (Snapshot{Plan: p, Expect: p.Expect(info, 0), Verify: v}).Fingerprint() == a {
		t.Error("another bits-per-pixel threshold must change the fingerprint")
	}
}

// fixture parses a probe from internal/media/testdata.
func fixture(t *testing.T, rel string) media.Info {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "media", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	info, err := media.ParseProbe(b)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Cover art first, as media managers write it: eligibility uses the main video's own bitrate, not the
// file's headline rate (which belongs to the first video stream: the picture).
func TestSliderUsesTheMainVideosBitrate(t *testing.T) {
	info := fixture(t, "crafted/cover_art_first.json")
	v := info.Video()
	if v == nil || v.BitrateKbps != 5000 || info.MainVideoOrdinal() != 1 {
		t.Fatalf("fixture changed: %+v", v)
	}
	p := SliderTemplate(3).Compile(info)
	if strings.Contains(p.Skip, "unknown") || !strings.Contains(p.Reason+p.Skip, "5000 kbps") && !strings.Contains(p.Skip, "already efficient") {
		t.Errorf("skip %q reason %q: the main stream's 5000 kbps must be judged", p.Skip, p.Reason)
	}
	if p.Video.Index != 1 {
		t.Errorf("encodes video stream %d, want the main one (1)", p.Video.Index)
	}
	// a stream without its own bitrate falls back to the file's average
	noRate := probe("h264", 1920, 1080, 24, 0)
	noRate.SizeBytes, noRate.DurationSec = 75_000_000, 60 // 10000 kbps on average
	if p := SliderTemplate(3).Compile(noRate); p.Skip != "" || !strings.Contains(p.Reason, "10000 kbps") {
		t.Errorf("fallback: skip %q reason %q", p.Skip, p.Reason)
	}
}

// ffprobe names MP4/MOV by their alias list; the subtitle check must see through it.
func TestSliderSubtitleCheckWithTheRealMP4Name(t *testing.T) {
	info := fixture(t, "real/plain.json")
	if !strings.Contains(info.Container, "mp4") || info.Container == "mp4" {
		t.Fatalf("fixture's format_name is %q", info.Container)
	}
	pgs := info
	pgs.Streams = append(append([]media.Stream{}, info.Streams...), media.Stream{Type: "subtitle", Codec: "hdmv_pgs_subtitle"})
	if p := SliderTemplate(1).Compile(pgs); !strings.Contains(p.Skip, "hdmv_pgs_subtitle subtitles cannot be kept") {
		t.Errorf("PGS in %q: skip %q", info.Container, p.Skip)
	}
	movText := info
	movText.Streams = append(append([]media.Stream{}, info.Streams...), media.Stream{Type: "subtitle", Codec: "mov_text"})
	if p := SliderTemplate(1).Compile(movText); strings.Contains(p.Skip, "subtitles") {
		t.Errorf("mov_text in %q: skip %q", info.Container, p.Skip)
	}
	mkv := pgs
	mkv.Container = "matroska,webm"
	if p := SliderTemplate(1).Compile(mkv); strings.Contains(p.Skip, "subtitles") {
		t.Errorf("PGS in mkv: skip %q", p.Skip)
	}
}

// Every skip has a code the Preview can count, and a converted file has none; the code is not part
// of the fingerprint, so jobs recorded before it existed keep theirs.
func TestEverySkipHasACode(t *testing.T) {
	codes := map[string]bool{SkipAlreadyEfficient: true, SkipAlreadyHEVC: true, SkipOtherFormat: true, SkipNoVideo: true,
		SkipUnknownBitrate: true, SkipCannotKeepTrack: true, SkipSettingCannotRun: true, SkipHDR: true}
	tmpls := []Template{LegacyTemplate(Default(), "replace"), SliderTemplate(9), {Kind: KindCustom}}
	for q := 1; q <= 5; q++ {
		tmpls = append(tmpls, SliderTemplate(q))
	}
	for _, tmpl := range tmpls {
		for name, info := range probeSet(t) {
			p := tmpl.Compile(info)
			if (p.Skip == "") != (p.SkipCode == "") || p.SkipCode != "" && !codes[p.SkipCode] {
				t.Errorf("%+v %s: skip %q code %q", tmpl, name, p.Skip, p.SkipCode)
			}
		}
	}
	if p := LegacyTemplate(Default(), "replace").Compile(media.Info{VideoCodec: "hevc", BitrateKbps: 9000}); p.SkipCode != SkipAlreadyHEVC {
		t.Errorf("legacy hevc: %q", p.SkipCode)
	}
	if p := LegacyTemplate(Default(), "replace").Compile(media.Info{VideoCodec: "h264", BitrateKbps: 100}); p.SkipCode != SkipAlreadyEfficient {
		t.Errorf("legacy low bitrate: %q", p.SkipCode)
	}
	a, b := Snapshot{Plan: Plan{SkipCode: "x"}}, Snapshot{}
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("the skip code is not a setting")
	}
}
