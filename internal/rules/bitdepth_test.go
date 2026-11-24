// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// bitDepthSource is a 1080p H.264 probe at the given depth.
func bitDepthSource(depth int) media.Info {
	src := probe("h264", 1920, 1080, 24, 12000)
	src.Streams[0].BitDepth = depth
	return src
}

// The Convert step's keep (and absent) is today's plan, byte for byte, on 8-bit and 10-bit sources; so is 8 on an
// 8-bit source, which changes nothing and is not stored.
func TestBitDepthKeepIsTodaysPlan(t *testing.T) {
	for _, depth := range []int{8, 10} {
		src := bitDepthSource(depth)
		want := bitDepthPlanJSON(t, standard(func(*Selectors) {}).Compile(src))
		for _, choice := range []string{"keep", ""} {
			if got := bitDepthPlanJSON(t, standard(func(s *Selectors) { s.BitDepth = choice }).Compile(src)); got != want {
				t.Errorf("%d-bit source, bit depth %q: plan differs from keep:\n%s\n%s", depth, choice, got, want)
			}
		}
		if depth == 8 {
			if got := bitDepthPlanJSON(t, standard(func(s *Selectors) { s.BitDepth = "8" }).Compile(src)); got != want {
				t.Errorf("8 of an 8-bit source changes the plan: %s", got)
			}
		}
	}
}

// 8 on a 10-bit SDR source: the processor writes 8-bit frames, the card's arguments carry no 10-bit flags, and the
// plan is not a 10-bit one anywhere (NeedsTenBit, OutputDepth and EncodeTenBit agree).
func TestBitDepthEightMakesATenBitSDRSourceEightBit(t *testing.T) {
	src := bitDepthSource(10)
	p := standard(func(s *Selectors) { s.BitDepth = "8" }).Compile(src)
	if p.Video.TenBit || p.EncodeTenBit() || NeedsTenBit(p, &src) || OutputDepth(p, &src, "") != 8 || OutputDepth(p, &src, "hevc_qsv") != 8 {
		t.Fatalf("8 on 10-bit SDR: TenBit %v, EncodeTenBit %v, NeedsTenBit %v", p.Video.TenBit, p.EncodeTenBit(), NeedsTenBit(p, &src))
	}
	args := p.FFmpegArgs("in.mkv", "out.mkv")
	if !has(args, "-pix_fmt:v:0", "yuv420p") || hasFlag(args, "-profile:v:0") {
		t.Errorf("processor args for 8 on 10-bit SDR: %v", args)
	}
	// the card: the same plan with a graphics encoder carries no main10 and no p010 frames
	gp := gpuPlan(22, func(v *VideoEncode) { v.BitDepth = 8 })
	for _, enc := range []string{"hevc_nvenc", "hevc_qsv", "hevc_amf"} {
		args, ok := gp.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || hasFlag(args, "-profile:v:0") || !has(args, "-pix_fmt:v:0", "nv12") {
			t.Errorf("%s: 8 on a 10-bit SDR source: want nv12 and no main10: ok %v, %v", enc, ok, args)
		}
	}
	// VAAPI's upload already names nv12 for 8-bit frames, so it gets no -pix_fmt of its own
	if args, ok := gp.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi"); !ok || !has(args, "-filter:v:0", "format=nv12,hwupload") || hasFlag(args, "-profile:v:0") {
		t.Errorf("hevc_vaapi: 8 on a 10-bit SDR source: want format=nv12,hwupload and no main10: ok %v, %v", ok, args)
	}
	// the full pipeline's scale names 8-bit frames, so a decoder's 10-bit frames are made 8-bit
	gp.Video.Height = 1080
	args, ok := gp.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_qsv")
	if !ok || !has(args, "-filter:v:0", "scale_qsv=w=trunc(1080*iw/ih/2+0.5)*2:h=1080:format=nv12") {
		t.Errorf("full pipeline scale for 8 on 10-bit: ok %v, %v", ok, args)
	}
}

// 8 on HDR stays 10-bit: the HDR arguments and the 10-bit encoder choice are the same as with keep.
func TestBitDepthEightKeepsHDRTenBit(t *testing.T) {
	src := bitDepthSource(10)
	hdr := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	keep := standard(func(*Selectors) {}).Compile(src)
	keep.Video.HDR = hdr
	eight := keep
	eight.Video.BitDepth = 8
	if !NeedsTenBit(eight, &src) || OutputDepth(eight, &src, "") != 10 || OutputDepth(eight, &src, "hevc_qsv") != 10 {
		t.Errorf("8 on HDR: NeedsTenBit %v, processor depth %d, card depth %d",
			NeedsTenBit(eight, &src), OutputDepth(eight, &src, ""), OutputDepth(eight, &src, "hevc_qsv"))
	}
	if got, want := planArgs(eight), planArgs(keep); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("8 on HDR changes the arguments:\n%v\n%v", got, want)
	}
}

// 10 on an 8-bit HEVC or AV1 encode: the plan is 10-bit everywhere, the processor's frames are 10-bit, and the card
// gets main10 (HEVC) or p010 (AV1) the way a 10-bit SDR source does.
func TestBitDepthTenMakesAnEightBitSourceTenBit(t *testing.T) {
	src := bitDepthSource(8)
	hevc := standard(func(s *Selectors) { s.BitDepth = "10" }).Compile(src)
	if !hevc.Video.TenBit || !NeedsTenBit(hevc, &src) || !hevc.EncodeTenBit() || OutputDepth(hevc, &src, "") != 10 || OutputDepth(hevc, &src, "hevc_qsv") != 10 {
		t.Errorf("10 on 8-bit HEVC: TenBit %v, NeedsTenBit %v, EncodeTenBit %v", hevc.Video.TenBit, NeedsTenBit(hevc, &src), hevc.EncodeTenBit())
	}
	if args := hevc.FFmpegArgs("in.mkv", "out.mkv"); !has(args, "-pix_fmt:v:0", "yuv420p10le") {
		t.Errorf("processor args for 10 on 8-bit HEVC: %v", args)
	}
	gp := gpuPlan(22, func(v *VideoEncode) { v.TenBit, v.BitDepth = true, 10 })
	if args, ok := gp.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_nvenc"); !ok || !has(args, "-profile:v:0", "main10") || !has(args, "-pix_fmt:v:0", "p010le") {
		t.Errorf("hevc_nvenc for 10 on 8-bit HEVC: ok %v, %v", ok, args)
	}
	// AV1 is not compiled in this version (Available is HEVC only), so its plan is built as Compile would build it
	av1 := gpuPlan(22, func(v *VideoEncode) { v.Codec, v.Format, v.TenBit, v.BitDepth = "libsvtav1", "av1", true, 10 })
	if !av1.EncodeTenBit() || OutputDepth(av1, &src, "") != 10 || OutputDepth(av1, &src, "av1_nvenc") != 10 {
		t.Errorf("10 on 8-bit AV1: EncodeTenBit %v", av1.EncodeTenBit())
	}
	if args := av1.FFmpegArgs("in.mkv", "out.mkv"); !has(args, "-pix_fmt:v:0", "yuv420p10le") {
		t.Errorf("processor args for 10 on 8-bit AV1: %v", args)
	}
	if args, ok := av1.FFmpegArgsWith("in.mkv", "out.mkv", "av1_nvenc"); !ok || !has(args, "-pix_fmt:v:0", "p010le") || hasFlag(args, "-profile:v:0") {
		t.Errorf("av1_nvenc for 10 on 8-bit AV1: ok %v, %v", ok, args)
	}
}

// 10 is HEVC and AV1 only; 8 and keep are for any format; other values are invalid. Nothing is changed on refusal.
func TestBitDepthValidate(t *testing.T) {
	for _, format := range []string{"h264", "vp9"} {
		s := DefaultSelectors(3)
		s.Format, s.BitDepth = format, "10"
		err, ok := s.Validate().(*SelectorError)
		if !ok || err.Code != "not_available" || err.Message != "10-bit needs HEVC or AV1. Nothing was changed." {
			t.Errorf("10 on %s: %v", format, err)
		}
	}
	for _, c := range []struct {
		format, depth string
		ok            bool
	}{
		{"hevc", "10", true}, {"hevc", "8", true}, {"hevc", "keep", true}, {"hevc", "", true},
		{"hevc", "12", false}, {"hevc", "Keep", false},
	} {
		s := DefaultSelectors(3)
		s.Format, s.BitDepth = c.format, c.depth
		if err := s.Validate(); (err == nil) != c.ok {
			t.Errorf("%s with bit depth %q: Validate = %v, want ok %v", c.format, c.depth, err, c.ok)
		}
	}
}

// A copied video is the same bytes: a bit depth choice changes nothing, and the plan is the keep plan.
func TestBitDepthIgnoredOnACopy(t *testing.T) {
	src := probe("hevc", 1920, 1080, 24, 3000)
	src.Streams[0].BitDepth = 10
	want := bitDepthPlanJSON(t, standard(func(s *Selectors) { s.Quality = 1 }).Compile(src))
	for _, choice := range []string{"8", "10"} {
		got := bitDepthPlanJSON(t, standard(func(s *Selectors) { s.Quality, s.BitDepth = 1, choice }).Compile(src))
		if got != want {
			t.Errorf("a copy with bit depth %q differs from keep:\n%s\n%s", choice, got, want)
		}
	}
}

func bitDepthPlanJSON(t *testing.T, p Plan) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func planArgs(p Plan) []string { return p.FFmpegArgs("in.mkv", "out.mkv") }

// With no height to scale to, the full pipeline's format-only filter names the card's frames: nv12 for 8 bits and
// p010 for 10 bits, on the source's depth either way (a 10-bit choice of an 8-bit file gets p010 too).
func TestBitDepthCardFullPipelineWithoutScale(t *testing.T) {
	eight := gpuPlan(22, func(v *VideoEncode) { v.BitDepth = 8 })
	if args, ok := eight.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_qsv"); !ok || !has(args, "-filter:v:0", "vpp_qsv=format=nv12") {
		t.Errorf("8 on a 10-bit SDR source, full pipeline, no scale: want vpp_qsv=format=nv12: ok %v, %v", ok, args)
	}
	ten := gpuPlan(22, func(v *VideoEncode) { v.BitDepth = 10; v.TenBit = true })
	if args, ok := ten.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_qsv"); !ok || !has(args, "-filter:v:0", "vpp_qsv=format=p010le") {
		t.Errorf("10 on an 8-bit source, full pipeline, no scale: want vpp_qsv=format=p010le: ok %v, %v", ok, args)
	}
}
