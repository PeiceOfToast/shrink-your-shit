// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// hdr10Info is a 1080p 10-bit HDR10 file, the source the tone-mapping rules are about.
func hdr10Info() media.Info {
	info := baseInfo()
	info.Video[0].Width, info.Video[0].Height = 1920, 1080
	info.Video[0].BitDepth = 10
	info.Video[0].ColorPrimaries = "bt2020"
	info.Video[0].ColorTransfer = "smpte2084"
	info.Video[0].ColorSpace = "bt2020nc"
	return info
}

// TestBuildTonemapIsSDRBT709 is hdr: tonemap on the processor (L-HW-16): zscale and tonemap in the
// filter chain, an 8-bit output, and the output tagged SDR BT.709 with no HDR metadata carried.
func TestBuildTonemapIsSDRBT709(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.HDR = HDRTonemap
	res := buildOK(t, plan, hdr10Info(), cpuRung(CodecHEVC))

	vf, _ := flagValue(res.Argv, "-vf:v:0")
	for _, part := range []string{
		"zscale=tin=smpte2084:pin=bt2020:min=bt2020nc:t=linear:npl=100",
		"tonemap=tonemap=hable",
		"zscale=t=bt709:m=bt709:r=tv",
		"format=yuv420p",
	} {
		if !strings.Contains(vf, part) {
			t.Errorf("filter %q lacks %q", vf, part)
		}
	}
	for flag, want := range map[string]string{
		"-color_primaries:v:0": "bt709",
		"-color_trc:v:0":       "bt709",
		"-colorspace:v:0":      "bt709",
		"-pix_fmt:v:0":         "yuv420p",
	} {
		if v, _ := flagValue(res.Argv, flag); v != want {
			t.Errorf("%s = %q, want %q", flag, v, want)
		}
	}
	if hasFlag(res.Argv, "-x265-params:v:0") {
		t.Error("a tone-mapped encode carries HDR10 x265 parameters")
	}
	// The frame side data the encoder would write into the header is deleted before the chain (#855).
	for _, del := range []string{"type=MASTERING_DISPLAY_METADATA", "type=CONTENT_LIGHT_LEVEL"} {
		if !strings.Contains(vf, "sidedata=mode=delete:"+del) {
			t.Errorf("filter %q does not delete %s", vf, del)
		}
	}
	if hasFlag(res.Argv, "-profile:v:0") {
		t.Error("an 8-bit tone-mapped encode asks for main10")
	}
}

// TestBuildTonemapNeedsAnHDRSource: a tone-map asked on an SDR picture changes nothing.
func TestBuildTonemapNeedsAnHDRSource(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.HDR = HDRTonemap
	res := buildOK(t, plan, baseInfo(), cpuRung(CodecHEVC))
	if strings.Contains(strings.Join(res.Argv, " "), "tonemap") {
		t.Errorf("an SDR source got a tone-map: %v", res.Argv)
	}
}

// TestBuildTonemapKeepsTenBitWhenAsked: colour depth 10 on a tone-mapped picture is ten-bit output.
func TestBuildTonemapKeepsTenBitWhenAsked(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.HDR = HDRTonemap
	plan.Video.BitDepth = Depth10
	res := buildOK(t, plan, hdr10Info(), cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("pix_fmt = %q, want yuv420p10le", v)
	}
	vf, _ := flagValue(res.Argv, "-vf:v:0")
	if !strings.HasSuffix(vf, "format=yuv420p10le") {
		t.Errorf("filter %q does not end in the ten-bit format", vf)
	}
}

// TestBuildEightBitOnHDRStaysTenBit: colour depth 8 on an HDR picture that is kept is still ten-bit
// (L-HW-16); only tone-mapping makes it 8-bit.
func TestBuildEightBitOnHDRStaysTenBit(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.BitDepth = Depth8
	res := buildOK(t, plan, hdr10Info(), cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("pix_fmt = %q, want yuv420p10le", v)
	}
	if v, _ := flagValue(res.Argv, "-profile:v:0"); v != "main10" {
		t.Errorf("profile = %q, want main10", v)
	}
}

// TestBuildTenBitHDRIsHDR10 is the picture size and the 10-bit HDR keep together: a 1080 box on a
// 4K HDR10 file gives an even 1920x1080 ten-bit picture with its metadata.
func TestBuildTenBitHDRIsHDR10(t *testing.T) {
	info := hdr10Info()
	info.Video[0].Width, info.Video[0].Height = 3840, 2160
	plan := basePlan(CodecHEVC)
	plan.Video.Scale = Scale{Height: 1080}
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if vf, _ := flagValue(res.Argv, "-vf:v:0"); vf != "scale=1920:1080" {
		t.Errorf("filter = %q, want scale=1920:1080", vf)
	}
	params, _ := flagValue(res.Argv, "-x265-params:v:0")
	if !strings.Contains(params, "transfer=smpte2084") {
		t.Errorf("x265 params %q lack the HDR10 transfer", params)
	}
}
