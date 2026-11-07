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
// same decisions, reasons and ffmpeg arguments, for every probe.
func TestLegacyPlanIsExactlyTodaysBehaviour(t *testing.T) {
	for _, r := range []Rule{Default(), {FromCodec: "h264", MinBitrateKbps: 3000, CRF: 24, Preset: "fast"}, {FromCodec: "mpeg2video", MinBitrateKbps: 0, CRF: 30, Preset: "ultrafast"}} {
		for _, out := range []string{"replace", "beside"} {
			tmpl := LegacyTemplate(r, out)
			for name, info := range probeSet(t) {
				p := tmpl.Compile(info)
				ok, why := r.Match(info)
				if ok != (p.Skip == "") || (ok && p.Reason != why) || (!ok && p.Skip != why) {
					t.Errorf("%s %+v: plan skip %q reason %q; today match=%v %q", name, r, p.Skip, p.Reason, ok, why)
				}
				got := strings.Join(p.FFmpegArgs("/in dir/a.mkv", "/in dir/.a.1.syp-tmp/new.mkv"), " ")
				want := todayArgs("/in dir/a.mkv", "/in dir/.a.1.syp-tmp/new.mkv", strconv.Itoa(r.CRF), r.Preset)
				if got != want {
					t.Fatalf("%s: args\n got %s\nwant %s", name, got, want)
				}
				if p.Output != out || p.MinSavingPct != 0 || p.MinSSIM != 0 {
					t.Errorf("%s: output %q, thresholds %d/%v: a legacy flow keeps the program's defaults", name, p.Output, p.MinSavingPct, p.MinSSIM)
				}
				if e := p.Expect(info, 100); e.MinSavings != 0 || e.MinSSIM != 0 {
					t.Errorf("%s: expect %+v", name, e)
				}
			}
		}
	}
}

func TestSliderPlanUsesTheTable(t *testing.T) {
	info := media.Info{VideoCodec: "h264", BitrateKbps: 8000}
	for _, pos := range Slider {
		p := SliderTemplate(pos.Position).Compile(info)
		if p.Skip != "" || p.Video.CRF != pos.CRF || p.Video.Preset != SliderPreset || p.MinSavingPct != pos.MinSavingPct || p.Output != "replace" {
			t.Errorf("position %d: %+v", pos.Position, p)
		}
		if e := p.Expect(info, 0); e.MinSavings != float64(pos.MinSavingPct)/100 {
			t.Errorf("position %d: expect min savings %v", pos.Position, e.MinSavings)
		}
	}
	if p := SliderTemplate(9).Compile(info); p.Skip == "" || p.Video.Codec != "" {
		t.Errorf("an unknown position cannot convert: %+v", p)
	}
	if p := (Template{Kind: KindCustom}).Compile(info); p.Skip == "" || p.Video.Codec != "" {
		t.Errorf("a custom flow cannot run yet: %+v", p)
	}
}

// The fingerprint names the conversion settings, not the file.
func TestPlanFingerprint(t *testing.T) {
	a := SliderTemplate(3).Compile(media.Info{VideoCodec: "h264", BitrateKbps: 8000})
	b := SliderTemplate(3).Compile(media.Info{VideoCodec: "h264", BitrateKbps: 12000})
	c := SliderTemplate(4).Compile(media.Info{VideoCodec: "h264", BitrateKbps: 8000})
	if a.Fingerprint() != b.Fingerprint() || a.Fingerprint() == c.Fingerprint() || len(a.Fingerprint()) != 16 {
		t.Errorf("same settings on two files: %s %s; other settings: %s", a.Fingerprint(), b.Fingerprint(), c.Fingerprint())
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
