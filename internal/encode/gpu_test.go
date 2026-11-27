// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"errors"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

const renderNode = "/dev/dri/renderD128"

// cardFamilies are the three card families this stage writes, with the device each needs.
var cardFamilies = []struct {
	name   string
	family Family
	device string
}{
	{"qsv", FamilyQSV, ""},
	{"vaapi", FamilyVAAPI, renderNode},
	{"nvenc", FamilyNVENC, ""},
}

// cardRung is one card family's hybrid rung: the processor decodes, the card encodes.
func cardRung(family Family, codec Codec, device string) Rung {
	encoder, _ := CardEncoder(family, codec)
	return Rung{Encoder: encoder, Family: family, Decode: DecodeSoftware, Device: device}
}

// fullRung is the full GPU pipeline: the card decodes and encodes.
func fullRung(family Family, codec Codec, device string) Rung {
	r := cardRung(family, codec, device)
	r.Decode = DecodeHardware
	return r
}

// argAt is the index of a flag, or -1.
func argAt(argv []string, flag string) int {
	for i, a := range argv {
		if a == flag {
			return i
		}
	}
	return -1
}

// wantPair asserts argv holds flag immediately followed by value, before limit (limit < 0 means
// anywhere).
func wantPair(t *testing.T, argv []string, flag, value string, limit int) {
	t.Helper()
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != flag {
			continue
		}
		if limit >= 0 && i >= limit {
			t.Errorf("%q is after position %d, want before it: %v", flag, limit, argv)
			return
		}
		if argv[i+1] != value {
			t.Errorf("%s = %q, want %q", flag, argv[i+1], value)
		}
		return
	}
	t.Errorf("%s is missing: %v", flag, argv)
}

// TestBuildSetsUpEachCardFamily: every card rung names its encoder and makes its device before the
// input it decodes (L-HW-10).
func TestBuildSetsUpEachCardFamily(t *testing.T) {
	for _, f := range cardFamilies {
		res := buildOK(t, basePlan(CodecHEVC), baseInfo(), cardRung(f.family, CodecHEVC, f.device))
		wantEncoder, _ := CardEncoder(f.family, CodecHEVC)
		if v, ok := flagValue(res.Argv, "-c:v:0"); !ok || v != wantEncoder {
			t.Errorf("%s: video codec = %q, want %q", f.name, v, wantEncoder)
		}
		input := argAt(res.Argv, "-i")
		if input < 0 {
			t.Fatalf("%s: no input in %v", f.name, res.Argv)
		}
		switch f.family {
		case FamilyQSV:
			wantPair(t, res.Argv, "-init_hw_device", "qsv=qsv:hw", input)
			wantPair(t, res.Argv, "-filter_hw_device", "qsv", input)
		case FamilyVAAPI:
			wantPair(t, res.Argv, "-init_hw_device", "vaapi=va:"+f.device, input)
			wantPair(t, res.Argv, "-filter_hw_device", "va", input)
		case FamilyNVENC:
			if argAt(res.Argv, "-init_hw_device") >= 0 {
				t.Errorf("nvenc takes the processor's frames, so it makes no device: %v", res.Argv)
			}
		}
	}
}

// TestQualityFlagsPerFamily: the same CRF reaches each encoder through its own flag (L-HW-10).
func TestQualityFlagsPerFamily(t *testing.T) {
	cases := []struct {
		name  string
		rung  Rung
		flags []string // flag, value, ...
	}{
		{"cpu", cpuRung(CodecHEVC), []string{"-crf:v:0", "22"}},
		{"qsv", cardRung(FamilyQSV, CodecHEVC, ""), []string{"-global_quality:v:0", "22"}},
		{"vaapi", cardRung(FamilyVAAPI, CodecHEVC, renderNode), []string{"-rc_mode:v:0", "CQP", "-qp:v:0", "22"}},
		{"nvenc", cardRung(FamilyNVENC, CodecHEVC, ""), []string{"-rc:v:0", "vbr", "-cq:v:0", "22"}},
	}
	for _, c := range cases {
		res := buildOK(t, basePlan(CodecHEVC), baseInfo(), c.rung)
		for i := 0; i < len(c.flags); i += 2 {
			if v, ok := flagValue(res.Argv, c.flags[i]); !ok || v != c.flags[i+1] {
				t.Errorf("%s: %s = %q (found %v), want %q", c.name, c.flags[i], v, ok, c.flags[i+1])
			}
		}
	}
}

// TestQSVHEVCOffsetPerSliderPosition: L-HW-12's measured offsets move QSV's HEVC quality number by
// slider position, and nothing else in the table does.
func TestQSVHEVCOffsetPerSliderPosition(t *testing.T) {
	for quality, want := range map[int]string{1: "20", 2: "20", 3: "21", 4: "21", 5: "21"} {
		plan := basePlan(CodecHEVC)
		plan.Video.Quality = quality
		res := buildOK(t, plan, baseInfo(), cardRung(FamilyQSV, CodecHEVC, ""))
		if v, _ := flagValue(res.Argv, "-global_quality:v:0"); v != want {
			t.Errorf("quality %d: global_quality = %q, want %q (CRF 22 plus the measured offset)", quality, v, want)
		}
	}
	// The measurements are QSV HEVC's alone.
	for _, c := range []struct {
		name  string
		codec Codec
		rung  Rung
		flag  string
	}{
		{"qsv-h264", CodecH264, cardRung(FamilyQSV, CodecH264, ""), "-global_quality:v:0"},
		{"vaapi-hevc", CodecHEVC, cardRung(FamilyVAAPI, CodecHEVC, renderNode), "-qp:v:0"},
		{"nvenc-hevc", CodecHEVC, cardRung(FamilyNVENC, CodecHEVC, ""), "-cq:v:0"},
	} {
		plan := basePlan(c.codec)
		plan.Video.Quality = 1
		res := buildOK(t, plan, baseInfo(), c.rung)
		if v, _ := flagValue(res.Argv, c.flag); v != "22" {
			t.Errorf("%s: %s = %q, want the CRF unchanged at 22", c.name, c.flag, v)
		}
	}
	if got := CardQualityOffsets()["qsv"]["1"]; got != -2 {
		t.Errorf("the editor's table has qsv position 1 at %d, want -2: one source for both", got)
	}
}

// TestFFmpegArgsFullGPUHasNoUpload: the full pipeline keeps the decoder's frames on the card, so
// its chain holds a card filter and no upload at all (L-HW-11).
func TestFFmpegArgsFullGPUHasNoUpload(t *testing.T) {
	for _, f := range []struct {
		name   string
		family Family
		device string
		filter string
		decode []string
	}{
		{
			"qsv", FamilyQSV, "",
			"scale_qsv=w=trunc(720*iw/ih/2+0.5)*2:h=720",
			[]string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"},
		},
		{
			"vaapi", FamilyVAAPI, renderNode,
			"scale_vaapi=w=-2:h=720:format=nv12",
			[]string{"-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi"},
		},
	} {
		plan := basePlan(CodecHEVC)
		plan.Video.Scale = Scale{Height: 720}
		res := buildOK(t, plan, baseInfo(), fullRung(f.family, CodecHEVC, f.device))
		if v, _ := flagValue(res.Argv, "-vf:v:0"); v != f.filter {
			t.Errorf("%s: filter = %q, want %q", f.name, v, f.filter)
		}
		for _, a := range res.Argv {
			if strings.Contains(a, "hwupload") {
				t.Errorf("%s: %q is in the full pipeline, and nothing may be uploaded", f.name, a)
			}
		}
		before := argAt(res.Argv, "-i")
		for i := 0; i < len(f.decode); i += 2 {
			wantPair(t, res.Argv, f.decode[i], f.decode[i+1], before)
		}
	}
}

// TestFFmpegArgsFullGPUOnlyForQSVAndVAAPI: NVENC never decodes (L-HW-17), and the processor is not
// a card (L-HW-11).
func TestFFmpegArgsFullGPUOnlyForQSVAndVAAPI(t *testing.T) {
	for _, f := range cardFamilies {
		_, err := Build(basePlan(CodecHEVC), baseInfo(), fullRung(f.family, CodecHEVC, f.device))
		if f.family == FamilyNVENC {
			if !errors.Is(err, ErrRungNotBuilt) {
				t.Errorf("nvenc: error = %v, want ErrRungNotBuilt for hardware decode", err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: error = %v, want the full pipeline built", f.name, err)
		}
	}
	cpu := cpuRung(CodecHEVC)
	cpu.Decode = DecodeHardware
	if _, err := Build(basePlan(CodecHEVC), baseInfo(), cpu); !errors.Is(err, ErrRungNotBuilt) {
		t.Errorf("cpu hardware decode: error = %v, want ErrRungNotBuilt", err)
	}
}

// TestQSVScaleWidthForSquarishShape: scale_qsv computes the even width the processor's scaler would
// have written from the upright picture, so a 4:3 source is not squashed (L-HW-11).
func TestQSVScaleWidthForSquarishShape(t *testing.T) {
	info := baseInfo()
	info.Video[0].Width, info.Video[0].Height = 1440, 1080 // 4:3
	plan := basePlan(CodecHEVC)
	plan.Video.Scale = Scale{Height: 720}
	res := buildOK(t, plan, info, fullRung(FamilyQSV, CodecHEVC, ""))
	want := "scale_qsv=w=trunc(720*iw/ih/2+0.5)*2:h=720"
	if v, _ := flagValue(res.Argv, "-vf:v:0"); v != want {
		t.Errorf("filter = %q, want %q", v, want)
	}
	// The processor's own scaler for the same box writes 960x720; the expression computes the
	// same picture, so the two rungs do not disagree about the width.
	width, height, err := scaleTo(info.Video[0], plan.Video.Scale)
	if err != nil || width != 960 || height != 720 {
		t.Fatalf("scaleTo = %dx%d, %v; want 960x720", width, height, err)
	}
}

// TestMaxFrameRateCardPaths: a frame rate change keeps the rung hybrid, where -r is written; the
// full pipeline refuses it (L-HW-11).
func TestMaxFrameRateCardPaths(t *testing.T) {
	for _, f := range cardFamilies {
		plan := basePlan(CodecHEVC)
		plan.Video.FPS = 30
		res := buildOK(t, plan, baseInfo(), cardRung(f.family, CodecHEVC, f.device))
		if v, _ := flagValue(res.Argv, "-r:v:0"); v != "30" {
			t.Errorf("%s: -r = %q, want 30", f.name, v)
		}
	}
	for _, f := range cardFamilies {
		if f.family == FamilyNVENC {
			continue // already refused for every full-pipeline plan
		}
		plan := basePlan(CodecHEVC)
		plan.Video.FPS = 30
		if _, err := Build(plan, baseInfo(), fullRung(f.family, CodecHEVC, f.device)); !errors.Is(err, ErrRungNotBuilt) {
			t.Errorf("%s: error = %v, want the full pipeline refused for a frame rate change", f.name, err)
		}
	}
}

// TestFFmpegArgsWithScaleAndVAAPIFilterOrder: in the hybrid path the processor's scale runs first
// and the upload is last, and a card filter never runs before the frames are uploaded (L-HW-11).
func TestFFmpegArgsWithScaleAndVAAPIFilterOrder(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.Scale = Scale{Height: 720}
	res := buildOK(t, plan, baseInfo(), cardRung(FamilyVAAPI, CodecHEVC, renderNode))
	if v, _ := flagValue(res.Argv, "-vf:v:0"); v != "scale=1280:720,format=nv12,hwupload" {
		t.Errorf("filter = %q, want the processor's scale, the format, then the upload", v)
	}

	ten := baseInfo()
	ten.Video[0].BitDepth = 10
	res = buildOK(t, plan, ten, cardRung(FamilyVAAPI, CodecHEVC, renderNode))
	if v, _ := flagValue(res.Argv, "-vf:v:0"); v != "scale=1280:720,format=p010,hwupload" {
		t.Errorf("ten-bit filter = %q, want p010 on the upload", v)
	}

	// A ten-bit source asked to be eight bit is converted before the upload, not after.
	eight := plan
	eight.Video.BitDepth = Depth8
	res = buildOK(t, eight, ten, cardRung(FamilyVAAPI, CodecHEVC, renderNode))
	if v, _ := flagValue(res.Argv, "-vf:v:0"); v != "scale=1280:720,format=nv12,hwupload" {
		t.Errorf("depth change filter = %q, want nv12 on the upload", v)
	}
}

// TestCardTenBitPixelFormats: QSV and VA-API name the format on the frames, and NVENC takes the
// processor's p010le (L-HW-10, L-HW-13).
func TestCardTenBitPixelFormats(t *testing.T) {
	info := baseInfo()
	info.Video[0].BitDepth = 10
	for _, f := range []struct {
		name   string
		family Family
		device string
		filter string
	}{
		{"qsv", FamilyQSV, "", "format=p010,hwupload=extra_hw_frames=64"},
		{"vaapi", FamilyVAAPI, renderNode, "format=p010,hwupload"},
	} {
		res := buildOK(t, basePlan(CodecHEVC), info, cardRung(f.family, CodecHEVC, f.device))
		if v, _ := flagValue(res.Argv, "-vf:v:0"); v != f.filter {
			t.Errorf("%s: filter = %q, want %q", f.name, v, f.filter)
		}
		if _, ok := flagValue(res.Argv, "-pix_fmt:v:0"); ok {
			t.Errorf("%s: -pix_fmt is the processor's flag; the frames name their own format", f.name)
		}
	}
	res := buildOK(t, basePlan(CodecHEVC), info, cardRung(FamilyNVENC, CodecHEVC, ""))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "p010le" {
		t.Errorf("nvenc: -pix_fmt = %q, want p010le", v)
	}
	res = buildOK(t, basePlan(CodecHEVC), baseInfo(), cardRung(FamilyNVENC, CodecHEVC, ""))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p" {
		t.Errorf("nvenc eight-bit: -pix_fmt = %q, want yuv420p", v)
	}
	if v, _ := flagValue(res.Argv, "-profile:v:0"); v != "" {
		t.Errorf("nvenc eight-bit: profile = %q, want none", v)
	}
}

// TestFullGPURefusesAChangeThatNeedsTheProcessor: frame rate, tone mapping and a depth change each
// push work through the processor, so the rung falls back to hybrid — where the same plan builds
// (L-HW-11).
func TestFullGPURefusesAChangeThatNeedsTheProcessor(t *testing.T) {
	hdr := goldenInfo(10, "smpte2084")
	for _, f := range cardFamilies {
		if f.family == FamilyNVENC {
			continue // already refused for every full-pipeline plan
		}
		sdr10 := goldenInfo(10, "bt709")
		cases := []struct {
			name string
			plan Plan
			info media.Info
		}{
			{"frame rate", func() Plan { p := basePlan(CodecHEVC); p.Video.FPS = 30; return p }(), baseInfo()},
			{"tone mapping", func() Plan { p := basePlan(CodecHEVC); p.Video.HDR = HDRTonemap; return p }(), hdr},
			{"depth change", func() Plan { p := basePlan(CodecHEVC); p.Video.BitDepth = Depth8; return p }(), sdr10},
		}
		for _, c := range cases {
			if _, err := Build(c.plan, c.info, fullRung(f.family, CodecHEVC, f.device)); !errors.Is(err, ErrRungNotBuilt) {
				t.Errorf("%s/%s: error = %v, want the full pipeline refused", f.name, c.name, err)
			}
			if _, err := Build(c.plan, c.info, cardRung(f.family, CodecHEVC, f.device)); err != nil {
				t.Errorf("%s/%s: hybrid rung = %v, want the file to still convert", f.name, c.name, err)
			}
		}
	}
}

// TestFullGPUKeepsTenBits: a ten-bit source whose output keeps ten bits needs no processor filter,
// so the full pipeline writes no -vf and asks for the ten-bit profile (L-HW-11).
func TestFullGPUKeepsTenBits(t *testing.T) {
	info := goldenInfo(10, "bt709")
	for _, f := range cardFamilies[0:2] {
		res := buildOK(t, basePlan(CodecHEVC), info, fullRung(f.family, CodecHEVC, f.device))
		if v, ok := flagValue(res.Argv, "-vf:v:0"); ok {
			t.Errorf("%s: filter = %q, want none (the decoder's ten-bit frames need no conversion)", f.name, v)
		}
		if v, _ := flagValue(res.Argv, "-profile:v:0"); v != "main10" {
			t.Errorf("%s: profile = %q, want main10", f.name, v)
		}
	}
}

// TestVAAPINeedsItsRenderNode: the device is what the encoder runs on, so a plan without one is a
// plan error rather than a guess (L-HW-10).
func TestVAAPINeedsItsRenderNode(t *testing.T) {
	rung := cardRung(FamilyVAAPI, CodecHEVC, "")
	if _, err := Build(basePlan(CodecHEVC), baseInfo(), rung); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("error = %v, want ErrInvalidPlan", err)
	}
}

// TestRateControlOnACardIsRefused: the bitrate flags are not part of this stage, and a number the
// person chose is not silently dropped (L-HW-10 keeps quality only).
func TestRateControlOnACardIsRefused(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.CRF = nil
	plan.RateControl = &RateControl{Mode: "vbr", AvgMbps: 5, MaxMbps: 7}
	for _, f := range cardFamilies {
		if _, err := Build(plan, baseInfo(), cardRung(f.family, CodecHEVC, f.device)); !errors.Is(err, ErrNotBuiltYet) {
			t.Errorf("%s: error = %v, want ErrNotBuiltYet", f.name, err)
		}
	}
}

// TestNvencProfileMatchesTheSwitches: the ten-bit pixel format, the main10 profile and the rate
// control are one switch set; eight bits carries none of the profile (L-HW-10).
func TestNvencProfileMatchesTheSwitches(t *testing.T) {
	ten := baseInfo()
	ten.Video[0].BitDepth = 10
	res := buildOK(t, basePlan(CodecHEVC), ten, cardRung(FamilyNVENC, CodecHEVC, ""))
	for flag, want := range map[string]string{
		"-pix_fmt:v:0": "p010le", "-profile:v:0": "main10",
		"-rc:v:0": "vbr", "-cq:v:0": "22",
	} {
		if v, ok := flagValue(res.Argv, flag); !ok || v != want {
			t.Errorf("ten bits: %s = %q (found %v), want %q", flag, v, ok, want)
		}
	}
	res = buildOK(t, basePlan(CodecHEVC), baseInfo(), cardRung(FamilyNVENC, CodecHEVC, ""))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p" {
		t.Errorf("eight bits: pix_fmt = %q, want yuv420p", v)
	}
	if _, ok := flagValue(res.Argv, "-profile:v:0"); ok {
		t.Errorf("eight bits: a profile is written with no main10 to ask for: %v", res.Argv)
	}
}

// TestCardCRFCarriesOnlyMeasured: only QSV HEVC has an offset, so NVENC and VA-API carry the CRF
// unchanged at every slider position (L-HW-12).
func TestCardCRFCarriesOnlyMeasured(t *testing.T) {
	for _, f := range []struct {
		name string
		rung Rung
		flag string
	}{
		{"vaapi", cardRung(FamilyVAAPI, CodecHEVC, renderNode), "-qp:v:0"},
		{"nvenc", cardRung(FamilyNVENC, CodecHEVC, ""), "-cq:v:0"},
	} {
		for quality := 1; quality <= 5; quality++ {
			plan := basePlan(CodecHEVC)
			plan.Video.Quality = quality
			res := buildOK(t, plan, baseInfo(), f.rung)
			if v, _ := flagValue(res.Argv, f.flag); v != "22" {
				t.Errorf("%s quality %d: %s = %q, want the CRF unchanged at 22 (no measured offset)",
					f.name, quality, f.flag, v)
			}
		}
	}
}

// TestCompiledCardCRFIsHEVCOnly: the offset table is read for QSV HEVC alone, and a plan that does
// not say its slider position gets no offset (L-HW-12).
func TestCompiledCardCRFIsHEVCOnly(t *testing.T) {
	for _, c := range []struct {
		family Family
		codec  Codec
	}{
		{FamilyQSV, CodecH264}, {FamilyQSV, CodecAV1},
		{FamilyVAAPI, CodecHEVC}, {FamilyNVENC, CodecHEVC}, {FamilyCPU, CodecHEVC},
	} {
		for quality := 1; quality <= 5; quality++ {
			if got := cardQualityOffset(c.family, c.codec, quality); got != 0 {
				t.Errorf("%s/%s quality %d: offset %d, want 0", c.family, c.codec, quality, got)
			}
		}
	}
	if got := cardQualityOffset(FamilyQSV, CodecHEVC, 0); got != 0 {
		t.Errorf("quality 0 (unset): offset %d, want 0", got)
	}
	if got := cardQualityOffset(FamilyQSV, CodecHEVC, 6); got != 0 {
		t.Errorf("quality 6 (out of range): offset %d, want 0", got)
	}
}

// TestAV1PlanDropsSVTParamsOnTheGraphicsEncoder: -svtav1-params belongs to libsvtav1, so a card's
// AV1 rung does not carry it (L-HW-13).
func TestAV1PlanDropsSVTParamsOnTheGraphicsEncoder(t *testing.T) {
	cpu := buildOK(t, basePlan(CodecAV1), baseInfo(), cpuRung(CodecAV1))
	if v, ok := flagValue(cpu.Argv, "-svtav1-params:v:0"); !ok || v != "tune=0" {
		t.Fatalf("cpu av1: svt params = %q (found %v), want tune=0", v, ok)
	}
	for _, f := range cardFamilies {
		res := buildOK(t, basePlan(CodecAV1), baseInfo(), cardRung(f.family, CodecAV1, f.device))
		if v, ok := flagValue(res.Argv, "-svtav1-params:v:0"); ok {
			t.Errorf("%s av1: carries SVT parameters (%q); the card's encoder has no such option", f.name, v)
		}
		want, _ := CardEncoder(f.family, CodecAV1)
		if v, _ := flagValue(res.Argv, "-c:v:0"); v != want {
			t.Errorf("%s av1: encoder = %q, want %q", f.name, v, want)
		}
	}
}

// TestH264PlanUsesTheGraphicsEncoder: H.264 on a card is the card's own encoder, never libx264
// (L-HW-13).
func TestH264PlanUsesTheGraphicsEncoder(t *testing.T) {
	for _, f := range cardFamilies {
		res := buildOK(t, basePlan(CodecH264), baseInfo(), cardRung(f.family, CodecH264, f.device))
		want, _ := CardEncoder(f.family, CodecH264)
		if v, _ := flagValue(res.Argv, "-c:v:0"); v != want {
			t.Errorf("%s: encoder = %q, want %q", f.name, v, want)
		}
		if v, _ := flagValue(res.Argv, "-tag:v:0"); v != "avc1" {
			t.Errorf("%s: tag = %q, want avc1", f.name, v)
		}
	}
}

// TestVAAPIPresetIsSaidOutLoud: VA-API has no speed setting, so the choice is a note; the other
// families write -preset (L-HW-13).
func TestVAAPIPresetIsSaidOutLoud(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.Preset = "faster"

	res := buildOK(t, plan, baseInfo(), cardRung(FamilyVAAPI, CodecHEVC, renderNode))
	if _, ok := flagValue(res.Argv, "-preset:v:0"); ok {
		t.Error("vaapi: -preset is written, and the VA-API encoder has no such setting")
	}
	wantNotes(t, res, "The card's VA-API encoder has no speed setting, so this file encodes at the driver's own pace")

	for _, f := range cardFamilies[0:1] { // qsv
		r := buildOK(t, plan, baseInfo(), cardRung(f.family, CodecHEVC, f.device))
		if v, _ := flagValue(r.Argv, "-preset:v:0"); v != "fast" {
			t.Errorf("%s: -preset = %q, want fast", f.name, v)
		}
		wantNotes(t, r)
	}
	r := buildOK(t, plan, baseInfo(), cardRung(FamilyNVENC, CodecHEVC, ""))
	if v, _ := flagValue(r.Argv, "-preset:v:0"); v != "fast" {
		t.Errorf("nvenc: -preset = %q, want fast", v)
	}

	// The default choice is not worth a note.
	plan.Video.Preset = "normal"
	res = buildOK(t, plan, baseInfo(), cardRung(FamilyVAAPI, CodecHEVC, renderNode))
	wantNotes(t, res)
}
