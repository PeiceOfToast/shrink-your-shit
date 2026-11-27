// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"errors"
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

// TestBuildH264IsEightBit: H.264 is 8-bit by choice (#855). A ten-bit SDR source is written as 8-bit with
// the reason in the log; asking for ten-bit H.264, or keeping HDR in it, is refused.
func TestBuildH264IsEightBit(t *testing.T) {
	tenBitSDR := baseInfo()
	tenBitSDR.Video[0].BitDepth = 10
	res := buildOK(t, basePlan(CodecH264), tenBitSDR, cpuRung(CodecH264))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p" {
		t.Errorf("pix_fmt = %q, want yuv420p for H.264", v)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "written as 8-bit") {
		t.Errorf("notes = %q, want the 8-bit note", res.Notes)
	}

	plan := basePlan(CodecH264)
	plan.Video.BitDepth = Depth10
	if _, err := Build(plan, baseInfo(), cpuRung(CodecH264)); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("ten-bit H.264: error = %v, want ErrInvalidPlan", err)
	}

	hdr := plan
	hdr.Video.BitDepth = DepthSource
	if _, err := Build(hdr, hdr10Info(), cpuRung(CodecH264)); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("HDR kept in H.264: error = %v, want ErrInvalidPlan", err)
	}
}

// TestH264HDRIsRefusedInEveryShape: HDR in H.264 is refused at every scale and container, so no argument row can
// describe a ten-bit HDR H.264 file (#855).
func TestH264HDRIsRefusedInEveryShape(t *testing.T) {
	for _, scale := range []Scale{{}, {Height: 1080}, {Height: 720}} {
		for _, container := range []Container{ContainerMKV, ContainerMP4} {
			for _, info := range []media.Info{hdr10Info(), func() media.Info {
				i := hdr10Info()
				i.Video[0].ColorTransfer = "arib-std-b67"
				return i
			}()} {
				plan := basePlan(CodecH264)
				plan.Video.Scale = scale
				plan.Container = container
				if _, err := Build(plan, info, cpuRung(CodecH264)); !errors.Is(err, ErrInvalidPlan) {
					t.Errorf("H.264 with %s, scale %+v, %s: error = %v, want ErrInvalidPlan", info.Video[0].ColorTransfer, scale, container, err)
				}
			}
		}
	}
}

// TestScaleGivesEvenSidesFromOddSources: a picture with odd sides is scaled to even ones, the shape kept within a
// pixel (4:2:0 needs even sides).
func TestScaleGivesEvenSidesFromOddSources(t *testing.T) {
	cases := []struct {
		name         string
		srcW, srcH   int
		box          Scale
		wantW, wantH int
	}{
		{"odd 1281x721 to height 360", 1281, 721, Scale{Height: 360}, 640, 360},
		{"odd 1921x1081 to height 720", 1921, 1081, Scale{Height: 720}, 1278, 720},
		{"odd 853x481 to width 426", 853, 481, Scale{Width: 426}, 426, 240},
	}
	for _, c := range cases {
		info := baseInfo()
		info.Video[0].Width, info.Video[0].Height = c.srcW, c.srcH
		w, h, err := scaleTo(info.Video[0], c.box)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("%s: %dx%d has an odd side", c.name, w, h)
		}
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}
