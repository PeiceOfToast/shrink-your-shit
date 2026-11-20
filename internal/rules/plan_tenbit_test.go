// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"testing"
)

// A 10-bit SDR source is kept at its depth on a graphics encoder: the encoder-decode path asks for main10 and p010
// frames the way HDR does, with no colour tags (an SDR file stays SDR). Before this, the args carried neither and
// the card was fed 8-bit frames.
func TestFFmpegArgsWithTenBitSDR(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.TenBit = true })
	for _, enc := range []string{"hevc_nvenc", "hevc_qsv", "hevc_amf"} {
		args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || !has(args, "-profile:v:0", "main10") || !has(args, "-pix_fmt:v:0", "p010le") {
			t.Errorf("%s: ok %v, args %v", enc, ok, args)
		}
		for _, flag := range []string{"-color_primaries:v:0", "-color_trc:v:0", "-colorspace:v:0", "-x265-params:v:0"} {
			if hasFlag(args, flag) {
				t.Errorf("%s: a 10-bit SDR encode writes no colour tags, but has %s: %v", enc, flag, args)
			}
		}
	}
	// VAAPI's frames are made p010 by its upload filter, not by -pix_fmt
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-profile:v:0", "main10") || hasFlag(args, "-pix_fmt:v:0") || !has(args, "-filter:v:0", "format=p010,hwupload") {
		t.Errorf("vaapi: ok %v, args %v", ok, args)
	}
	// a downscale keeps p010 after the scale, as HDR's does
	p.Video.Height = 1080
	args, _ = p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !has(args, "-filter:v:0", "scale=-2:1080,format=p010,hwupload") {
		t.Errorf("vaapi downscale: %v", args)
	}
}

// AV1's profile 0 covers 10-bit, so its graphics encoders get p010 frames and no main10. H.264 has no 10-bit path
// here (#498 keeps it on the processor), so a 10-bit source made into H.264 gets today's 8-bit args.
func TestFFmpegArgsWithTenBitPerTarget(t *testing.T) {
	av1 := gpuPlan(22, func(v *VideoEncode) { v.Codec, v.Format, v.TenBit = "libsvtav1", "av1", true })
	args, ok := av1.FFmpegArgsWith("in.mkv", "out.mkv", "av1_nvenc")
	if !ok || !has(args, "-pix_fmt:v:0", "p010le") || hasFlag(args, "-profile:v:0") {
		t.Errorf("av1: ok %v, args %v", ok, args)
	}
	h264 := gpuPlan(22, func(v *VideoEncode) { v.Codec, v.Format, v.TenBit = "libx264", "h264", true })
	for _, enc := range []string{"h264_nvenc", "h264_qsv", "h264_vaapi", "h264_amf"} {
		args, ok := h264.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || hasFlag(args, "-profile:v:0") || hasFlag(args, "-pix_fmt:v:0") || has(args, "-filter:v:0", "format=p010,hwupload") {
			t.Errorf("%s: a 10-bit source made into H.264 stays 8-bit: ok %v, args %v", enc, ok, args)
		}
	}
}

// The processor's encoder keeps the source's depth on its own (TestSliderKeepsTenBit), so its arguments are today's:
// adding -pix_fmt there would change every 10-bit SDR job that runs on the CPU.
func TestFFmpegArgsProcessorTenBitIsTodays(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.TenBit = true })
	args := p.FFmpegArgs("in.mkv", "out.mkv")
	if hasFlag(args, "-pix_fmt") || hasFlag(args, "-profile") {
		t.Errorf("the processor's 10-bit SDR args are today's: %v", args)
	}
}

// Compile carries the first video stream's depth into the Plan: a 10-bit source marks VideoEncode.TenBit on every
// flow, an 8-bit one does not, and a copied video is the same bytes so it is not marked.
func TestCompileCarriesTheSourcesDepth(t *testing.T) {
	ten := probe("h264", 1920, 1080, 24, 12000)
	ten.Streams[0].BitDepth = 10
	eight := probe("h264", 1920, 1080, 24, 12000)
	eight.Streams[0].BitDepth = 8
	for name, tmpl := range map[string]Template{
		"standard": standard(func(*Selectors) {}),
		"legacy":   LegacyTemplate(Default(), "replace"),
	} {
		if p := tmpl.Compile(ten); !p.Video.TenBit || !p.EncodeTenBit() {
			t.Errorf("%s: a 10-bit source is carried into the Plan: %+v", name, p.Video)
		}
		if p := tmpl.Compile(eight); p.Video.TenBit || p.EncodeTenBit() {
			t.Errorf("%s: an 8-bit source marks nothing: %+v", name, p.Video)
		}
	}
	// the copy: an HEVC source already in the target's format, at its size, with no cap
	copied := probe("hevc", 1920, 1080, 24, 3000)
	copied.Streams[0].BitDepth = 10
	if p := standard(func(s *Selectors) { s.Quality = 1 }).Compile(copied); !p.Video.Copy || p.Video.TenBit {
		t.Errorf("a copied video is not marked: %+v", p.Video)
	}
}
