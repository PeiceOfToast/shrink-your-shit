// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"slices"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// planArgv plans the Video step and builds the command the processor would run, so these tests
// check the ffmpeg arguments a setting becomes, not only the plan's fields.
func planArgv(t *testing.T, set VideoSettings, info media.Info) []string {
	t.Helper()
	p := encode.Plan{Container: encode.ContainerMKV, Output: "out.mkv"}
	d, err := VideoStep{}.Plan(set, flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !d.Work {
		t.Fatalf("decision = %+v, want work", d)
	}
	enc, _ := encode.CPUEncoder(encode.Codec(set.Format))
	res, err := encode.Build(p, info, encode.Rung{Encoder: enc, Family: encode.FamilyCPU, Decode: encode.DecodeSoftware})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return res.Argv
}

// argvValue is the value after a flag in an argument list, "" when the flag is absent.
func argvValue(argv []string, flag string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			return argv[i+1]
		}
	}
	return ""
}

// sourceAt returns the probe fixture at a frame rate, so the fps rules have a source rate.
func sourceAt(width, height int, fps int) media.Info {
	info := probeInfo("h264", width, height)
	info.Video[0].FrameRate = media.Rational{Num: fps, Den: 1}
	return info
}

// TestVideoPictureSizeIsAsked: the Size choice becomes the box; a custom size takes the typed sides.
func TestVideoPictureSizeIsAsked(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.Size = ptr("720")
	if got := sizeBox(set); got != (encode.Scale{Height: 720}) {
		t.Errorf("720 box = %+v, want height 720 with the width free", got)
	}
	set.Size = ptr("custom")
	set.Width = ptr(1280)
	if got := sizeBox(set); got != (encode.Scale{Width: 1280}) {
		t.Errorf("custom box = %+v, want width 1280 only", got)
	}
	set.Height = ptr(720)
	if got := sizeBox(set); got != (encode.Scale{Width: 1280, Height: 720}) {
		t.Errorf("custom box = %+v, want 1280x720", got)
	}
}

// TestVideoSizeIsScaledDownWithTheShape: a 1080p file to 720 is 1280x720 (size and aspect), even.
func TestVideoSizeIsScaledDownWithTheShape(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.Size = ptr("720")
	argv := planArgv(t, set, probeInfo("h264", 1920, 1080))
	if vf := argvValue(argv, "-vf:v:0"); vf != "scale=1280:720" {
		t.Errorf("filter = %q, want scale=1280:720", vf)
	}
}

// TestVideoSizeNeverUpscales: a 720p file asked for 1080 is not made bigger; no scale filter at all.
func TestVideoSizeNeverUpscales(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.Size = ptr("1080")
	argv := planArgv(t, set, probeInfo("h264", 1280, 720))
	if vf := argvValue(argv, "-vf:v:0"); vf != "" {
		t.Errorf("a 720p file got filter %q for a 1080 request", vf)
	}
}

// TestVideoFrameRateIsOnlyLowered: 60 asked of a 30 fps file keeps 30; 30 asked of a 60 fps file
// is 30; a file of unknown rate takes the number asked.
func TestVideoFrameRateIsOnlyLowered(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.FPS = ptr(60.0)
	if got := fpsAsked(set, sourceAt(1920, 1080, 30).Video[0]); got != 0 {
		t.Errorf("60 asked of 30 fps = %v, want 0 (keep the file's rate)", got)
	}
	set.FPS = ptr(30.0)
	if got := fpsAsked(set, sourceAt(1920, 1080, 60).Video[0]); got != 30 {
		t.Errorf("30 asked of 60 fps = %v, want 30", got)
	}
	if got := fpsAsked(set, probeInfo("h264", 1920, 1080).Video[0]); got != 30 {
		t.Errorf("30 asked of an unknown rate = %v, want 30", got)
	}
}

// TestVideoFrameRateSixtyToThirty is the real rule in the command: 60 → 30 writes -r 30.
func TestVideoFrameRateSixtyToThirty(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.FPS = ptr(30.0)
	argv := planArgv(t, set, sourceAt(1920, 1080, 60))
	if r := argvValue(argv, "-r:v:0"); r != "30" {
		t.Errorf("frame rate = %q, want 30", r)
	}
}

// TestVideoSpeedIsPerEncoder: "Slower, smaller" is each encoder's own name (L-HW-13).
func TestVideoSpeedIsPerEncoder(t *testing.T) {
	cases := []struct {
		format, want string
	}{
		{"hevc", "slow"},
		{"h264", "slow"},
		{"av1", "4"},
	}
	for _, c := range cases {
		set := videoSettings(c.format, 3)
		set.Preset = ptr("slower")
		// The source is another codec, so the step encodes rather than finding the file already done.
		src := "h264"
		if c.format == "h264" {
			src = "hevc"
		}
		argv := planArgv(t, set, probeInfo(src, 1920, 1080))
		if got := argvValue(argv, "-preset:v:0"); got != c.want {
			t.Errorf("%s slower: preset = %q, want %q", c.format, got, c.want)
		}
	}
}

// TestVideoHDRSkipLeavesTheFileAlone: an HDR file with hdr: skip is not converted, and the job says
// why; an SDR file under skip converts as usual.
func TestVideoHDRSkipLeavesTheFileAlone(t *testing.T) {
	info := probeInfo("h264", 1920, 1080)
	info.Video[0].ColorTransfer = "smpte2084"
	set := videoSettings("hevc", 3)
	set.HDR = ptr("skip")
	var p encode.Plan
	d, err := VideoStep{}.Plan(set, flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "video.hdr_skipped" {
		t.Errorf("decision = %+v, want no work with video.hdr_skipped", d)
	}
	if !strings.Contains(d.Label, "HDR10") {
		t.Errorf("label = %q, want the HDR kind named", d.Label)
	}

	sdr := probeInfo("h264", 1920, 1080)
	d, err = VideoStep{}.Plan(set, flow.PlanInput{Info: sdr}, &p)
	if err != nil || !d.Work {
		t.Errorf("SDR under skip: decision = %+v, err = %v; want the conversion", d, err)
	}
}

// TestVideoTonemapIsForHDROnly: tone-mapping is planned for an HDR file; an SDR file keeps the
// plan's keep.
func TestVideoTonemapIsForHDROnly(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.HDR = ptr("tonemap")

	hdr := probeInfo("h264", 1920, 1080)
	hdr.Video[0].ColorTransfer = "arib-std-b67"
	var p encode.Plan
	if _, err := (VideoStep{}).Plan(set, flow.PlanInput{Info: hdr}, &p); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Video.HDR != encode.HDRTonemap {
		t.Errorf("HLG under tonemap: HDR = %q, want tonemap", p.Video.HDR)
	}

	p = encode.Plan{}
	if _, err := (VideoStep{}).Plan(set, flow.PlanInput{Info: probeInfo("h264", 1920, 1080)}, &p); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.Video.HDR != encode.HDRKeep {
		t.Errorf("SDR under tonemap: HDR = %q, want keep", p.Video.HDR)
	}
}

// TestVideoColourDepthIsAsked: 10 and 8 are asked; unset keeps the source's own depth.
func TestVideoColourDepthIsAsked(t *testing.T) {
	if got := bitDepthAsked(VideoSettings{}); got != encode.DepthSource {
		t.Errorf("unset = %v, want the source's", got)
	}
	if got := bitDepthAsked(VideoSettings{BitDepth: ptr(10)}); got != encode.Depth10 {
		t.Errorf("10 = %v, want Depth10", got)
	}
	if got := bitDepthAsked(VideoSettings{BitDepth: ptr(8)}); got != encode.Depth8 {
		t.Errorf("8 = %v, want Depth8", got)
	}
	set := videoSettings("hevc", 3)
	set.BitDepth = ptr(10)
	argv := planArgv(t, set, probeInfo("h264", 1920, 1080))
	if v := argvValue(argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("pix_fmt = %q, want yuv420p10le", v)
	}
}

// TestVideoDolbyVisionIsGreyedNotFaked: keeping the RPU is greyed with its reason; the plan does
// not pretend to keep it.
func TestVideoDolbyVisionIsGreyedNotFaked(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.KeepDolbyVision = true
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("problems = %+v, want none: the setting is greyed", check.Problems)
	}
	found := slices.ContainsFunc(check.Disabled, func(d flow.Disabled) bool {
		return d.Field == "keepDolbyVision" && strings.Contains(d.Why, "not possible")
	})
	if !found {
		t.Errorf("disabled = %+v, want keepDolbyVision greyed with the reason", check.Disabled)
	}
}

// TestVideoTenBitH264IsGreyed: 10-bit H.264 is not offered (few devices play it).
func TestVideoTenBitH264IsGreyed(t *testing.T) {
	set := videoSettings("h264", 3)
	set.BitDepth = ptr(10)
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	found := slices.ContainsFunc(check.Disabled, func(d flow.Disabled) bool {
		return d.Field == "bitDepth" && strings.Contains(d.Why, "10-bit H.264")
	})
	if !found {
		t.Errorf("disabled = %+v, want 10-bit H.264 greyed", check.Disabled)
	}
}
