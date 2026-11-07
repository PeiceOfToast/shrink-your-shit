// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// S3 (the user's decision, RESEARCH/hdr-dv.md): HDR10 and HLG are converted to 10-bit HEVC with their
// HDR written out from the probe; Dolby Vision is left as it is unless the flow includes it, and
// profile 5 (or an unreadable profile) always is.

const testMD = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"

func hdr10(mod ...func(*media.Stream)) media.Info {
	i := h264(3840, 2160, 20000)
	i.BitrateKbps = 20000 // the legacy rule reads the file's bitrate
	v := &i.Streams[0]
	v.BitDepth, v.ColorTransfer, v.ColorPrimaries, v.ColorSpace = 10, "smpte2084", "bt2020", "bt2020nc"
	v.MasteringDisplay, v.CLL = testMD, "1000,400"
	for _, f := range mod {
		f(v)
	}
	return i
}

func stdDV(includeDV bool) Template {
	s := DefaultSelectors(3)
	s.IncludeDolbyVision = includeDV
	return s.Template()
}

func dv(profile int, transfer string) func(*media.Stream) {
	return func(v *media.Stream) { v.DolbyVision, v.DVProfile, v.ColorTransfer = true, profile, transfer }
}

func TestHDR10IsConvertedWithItsMetadata(t *testing.T) {
	p := SliderTemplate(3).Compile(hdr10())
	if p.Skip != "" || p.HDRNote != "" {
		t.Fatalf("%q %q", p.Skip, p.HDRNote)
	}
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	for _, want := range []string{
		"-pix_fmt:v:0 yuv420p10le", "-profile:v:0 main10",
		"-color_primaries:v:0 bt2020 -color_trc:v:0 smpte2084 -colorspace:v:0 bt2020nc",
		"-x265-params:v:0 colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:hdr10-opt=1:master-display=" + testMD + ":max-cll=1000,400",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in\n%s", want, args)
		}
	}
	e := p.Expect(hdr10(), 0)
	if e.HDR == nil || *e.HDR != (HDRExpect{Transfer: "smpte2084", Primaries: "bt2020", MasteringDisplay: true, CLL: true}) {
		t.Errorf("expect %+v", e.HDR)
	}
	// no metadata in the original: none is written, none required; primaries default to BT.2020
	bare := hdr10(func(v *media.Stream) { v.MasteringDisplay, v.CLL, v.ColorPrimaries, v.ColorSpace = "", "", "", "" })
	p = SliderTemplate(3).Compile(bare)
	args = strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if strings.Contains(args, "master-display") || strings.Contains(args, "max-cll") || !strings.Contains(args, "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1") {
		t.Errorf("bare HDR10: %s", args)
	}
	if e := p.Expect(bare, 0); e.HDR.MasteringDisplay || e.HDR.CLL {
		t.Errorf("bare expect %+v", e.HDR)
	}
}

func TestHLGAndHDR10Plus(t *testing.T) {
	hlg := hdr10(func(v *media.Stream) { v.ColorTransfer = "arib-std-b67" })
	p := SliderTemplate(3).Compile(hlg)
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if p.Skip != "" || !strings.Contains(args, "-x265-params:v:0 colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc -") || strings.Contains(args, "hdr10") {
		t.Errorf("HLG: %q %s", p.Skip, args) // HLG has no static metadata
	}
	plus := hdr10(func(v *media.Stream) { v.HDR10Plus = true })
	if p := SliderTemplate(3).Compile(plus); p.Skip != "" || p.HDRNote != HDR10PlusNote || p.Video.HDR.Transfer != "smpte2084" {
		t.Errorf("HDR10+: %q %q", p.Skip, p.HDRNote)
	}
}

func TestDolbyVision(t *testing.T) {
	for _, c := range []struct {
		name      string
		info      media.Info
		includeDV bool
		code      string
		note      string
	}{
		{"profile 8, left alone by default", hdr10(dv(8, "smpte2084")), false, SkipDV, ""},
		{"profile 8, included: HDR10 kept", hdr10(dv(8, "smpte2084")), true, "", DVRemovedNote},
		{"profile 7, included", hdr10(dv(7, "smpte2084")), true, "", DVRemovedNote},
		{"profile 8.4, included: HLG kept", hdr10(dv(8, "arib-std-b67")), true, "", DVRemovedHLG},
		{"profile 5, included: never", hdr10(dv(5, "")), true, SkipDVProfile5, ""},
		{"profile 5, not included", hdr10(dv(5, "")), false, SkipDV, ""},
		{"unknown profile (a dvh1 tag alone), included", hdr10(dv(0, "smpte2084")), true, SkipDVProfile5, ""},
		{"profile 8 without an HDR10 base in the stream, included", hdr10(dv(8, "")), true, SkipDVProfile5, ""},
	} {
		p := stdDV(c.includeDV).Compile(c.info)
		if p.SkipCode != c.code || p.HDRNote != c.note || (c.code == "" && p.Video.HDR == nil) {
			t.Errorf("%s: skip %q (%s) note %q", c.name, p.SkipCode, p.Skip, c.note)
		}
		if c.code != "" && SkipWords[c.code] == "" {
			t.Errorf("%s: no words for %s", c.name, c.code)
		}
	}
	// Basic never includes Dolby Vision; the legacy flow neither
	if p := SliderTemplate(3).Compile(hdr10(dv(8, "smpte2084"))); p.SkipCode != SkipDV {
		t.Errorf("Basic: %q", p.SkipCode)
	}
	if p := LegacyTemplate(Default(), "replace").Compile(hdr10(dv(8, "smpte2084"))); p.SkipCode != SkipDV {
		t.Errorf("legacy: %q", p.SkipCode)
	}
}

// Basic's repackaging (S4) copies the video, so its HDR stays as it is, and the new file must show it.
func TestRepackagedHDRKeepsItsHDR(t *testing.T) {
	i := hdr10(func(v *media.Stream) { v.Codec = "hevc" })
	i.VideoCodec, i.Container = "hevc", "mov,mp4,m4a,3gp,3g2,mj2"
	p := SliderTemplate(3).Compile(i)
	if p.Skip != "" || !p.Video.Copy || p.Video.HDR != nil {
		t.Fatalf("%q %+v", p.Skip, p.Video)
	}
	if e := p.Expect(i, 0); e.HDR == nil || *e.HDR != (HDRExpect{"smpte2084", "bt2020", true, true}) {
		t.Errorf("%+v", e.HDR)
	}
	i.Container = "matroska,webm"
	if p := SliderTemplate(3).Compile(i); p.SkipCode != SkipAlreadyInFormat {
		t.Errorf("HDR HEVC already in MKV: %q", p.SkipCode)
	}
}

// An SDR file converts exactly as before: no HDR arguments, nothing HDR expected.
func TestSDRIsUnchanged(t *testing.T) {
	p := SliderTemplate(3).Compile(h264(1920, 1080, 8000))
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if p.Video.HDR != nil || strings.Contains(args, "pix_fmt") || strings.Contains(args, "x265-params") || p.Expect(h264(1920, 1080, 8000), 0).HDR != nil {
		t.Errorf("%s", args)
	}
}

// The verify gate (S3): the new file must show the original's HDR, or it is refused.
func TestHDRIsCheckedInTheNewFile(t *testing.T) {
	e := Expect{HDR: &HDRExpect{Transfer: "smpte2084", Primaries: "bt2020", MasteringDisplay: true, CLL: true}}
	out := func(mod func(*media.Stream)) media.Info {
		i := hdr10(func(v *media.Stream) { v.Codec = "hevc" })
		mod(&i.Streams[0])
		return i
	}
	for name, c := range map[string]struct {
		mod  func(*media.Stream)
		want string
	}{
		"all kept":               {func(*media.Stream) {}, ""},
		"transfer lost":          {func(v *media.Stream) { v.ColorTransfer = "" }, "lost its HDR: its colour transfer is not set, not smpte2084"},
		"primaries changed":      {func(v *media.Stream) { v.ColorPrimaries = "bt709" }, "colour primaries are bt709"},
		"mastering display lost": {func(v *media.Stream) { v.MasteringDisplay = "" }, "mastering display"},
		"light levels lost":      {func(v *media.Stream) { v.CLL = "" }, "MaxCLL"},
	} {
		var hdrProblems []string
		for _, pr := range e.Compare(out(c.mod), -1) {
			if !strings.Contains(pr, "stream") && !strings.Contains(pr, "chapters") { // only the HDR lines
				hdrProblems = append(hdrProblems, pr)
			}
		}
		got := strings.Join(hdrProblems, "; ")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", name, got)
		}
	}
}
