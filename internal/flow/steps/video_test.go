// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// probeInfo is an ordinary 1080p H.264 file with two audio tracks and one subtitle.
func probeInfo(codec string, width, height int) media.Info {
	return media.Info{
		Path:      "/library/Movies/clip.mkv",
		Format:    media.Format{Names: []string{"matroska", "webm"}, Duration: 60_000_000_000},
		Video:     []media.VideoTrack{{Index: 0, Codec: codec, Width: width, Height: height, BitDepth: 8}},
		Audio:     []media.AudioTrack{{Index: 1, Codec: "eac3", Language: "eng"}, {Index: 2, Codec: "aac", Language: "ger"}},
		Subtitles: []media.SubtitleTrack{{Index: 3, Codec: "subrip", Language: "eng"}},
	}
}

func videoSettings(format string, quality int) VideoSettings {
	return VideoSettings{Format: format, Quality: quality, UseGpu: false, IfGpuFails: "cpu",
		BitrateLevel: "medium", Preset: ptr("normal"), HDR: ptr("keep")}
}

// TestVideoCRFTable is page 07's slider table: five positions of three formats, each moved by
// its size row (+1 at 2160p, 0 at 1440p and 1080p, -1 at 720p and sd).
func TestVideoCRFTable(t *testing.T) {
	// The page's processor column at 1080p, position 1 to 5.
	base := map[string][5]int{
		"hevc": {27, 25, 23, 21, 19},
		"h264": {22, 20, 18, 16, 14},
		"av1":  {36, 34, 32, 30, 28},
	}
	shift := map[string]int{"2160": 1, "1440": 0, "1080": 0, "720": -1, "sd": -1}
	for format, numbers := range base {
		for q := 1; q <= 5; q++ {
			for row, s := range shift {
				want := numbers[q-1] + s
				if got := videoCRF(format, q, row); got != want {
					t.Errorf("%s quality %d row %s: crf = %d, want %d", format, q, row, got, want)
				}
			}
		}
	}
}

// TestVideoPlansTheEncode fills the plan's video part and names the processor honestly.
func TestVideoPlansTheEncode(t *testing.T) {
	var p encode.Plan
	d, err := VideoStep{}.Plan(videoSettings("hevc", 3), flow.PlanInput{Info: probeInfo("h264", 1920, 1080)}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !d.Work || d.Reason != "video.convert" || d.Label != "Converting to HEVC" {
		t.Errorf("decision = %+v, want a convert", d)
	}
	if p.Video.Codec != encode.CodecHEVC || p.Video.CRF == nil || *p.Video.CRF != 23 {
		t.Errorf("video plan = %+v, want HEVC at CRF 23", p.Video)
	}
	if p.Video.BitDepth != encode.DepthSource || p.Video.HDR != encode.HDRKeep {
		t.Errorf("bit depth/HDR = %v/%q, want the source's", p.Video.BitDepth, p.Video.HDR)
	}
	if got := d.Detail["row"]; got != "1080" {
		t.Errorf("row = %v, want 1080", got)
	}
}

func TestVideoAlreadyTarget(t *testing.T) {
	var p encode.Plan
	d, err := VideoStep{}.Plan(videoSettings("hevc", 3), flow.PlanInput{Info: probeInfo("hevc", 1920, 1080)}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "video.already_target" || d.Label != "Already HEVC" {
		t.Errorf("decision = %+v, want already HEVC", d)
	}
	if p.Video.Codec != "" {
		t.Errorf("plan video = %+v, want untouched", p.Video)
	}
}

func TestVideoRowUsesTheUprightPicture(t *testing.T) {
	info := probeInfo("h264", 3840, 2160)
	info.Video[0].Rotation = 90 // upright 2160x3840: the 2160 row, not 1080
	if got := resolutionRow(info.Video[0]); got != "2160" {
		t.Errorf("row = %s, want 2160", got)
	}
	if got := resolutionRow(media.VideoTrack{Width: 640, Height: 480}); got != "sd" {
		t.Errorf("480p row = %s, want sd", got)
	}
}

func TestVideoWithoutAPictureIsAnError(t *testing.T) {
	info := media.Info{Path: "/library/clip.mkv", Audio: probeInfo("h264", 1, 1).Audio}
	var p encode.Plan
	_, err := VideoStep{}.Plan(videoSettings("hevc", 3), flow.PlanInput{Info: info}, &p)
	var pe *flow.PlanError
	if !errors.As(err, &pe) || pe.Code != "video.no_video" {
		t.Errorf("error = %v, want video.no_video", err)
	}
}

func TestVideoValidate(t *testing.T) {
	def := VideoStep{}.Def()
	check := VideoStep{}.Validate(videoSettings("hevc", 3),
		flow.ValidateContext{Env: flow.Env{Encoders: []string{"libx264", "libx265"}}})
	if len(check.Problems) != 0 {
		t.Errorf("problems = %+v, want none", check.Problems)
	}
	if check.Summary != "HEVC · quality 3 · graphics card off" {
		t.Errorf("summary = %q", check.Summary)
	}
	if len(check.Disabled) != 1 || check.Disabled[0].Field != "ifGpuFails" {
		t.Errorf("disabled = %+v, want ifGpuFails greyed when the card is off", check.Disabled)
	}

	// A worker list without the format's encoder greys that choice; the page names the missing name.
	check = VideoStep{}.Validate(videoSettings("av1", 3),
		flow.ValidateContext{Env: flow.Env{Encoders: []string{"libx264", "libx265"}}})
	if len(check.Disabled) == 0 || check.Disabled[0].Field != "format" || check.Disabled[0].Value != "av1" {
		t.Errorf("disabled = %+v, want the av1 choice greyed", check.Disabled)
	}

	// The card on with no card anywhere greys useGpu.
	gpu := videoSettings("hevc", 3)
	gpu.UseGpu = true
	check = VideoStep{}.Validate(gpu, flow.ValidateContext{Env: flow.Env{Encoders: []string{"libx265"}}})
	if len(check.Disabled) != 1 || check.Disabled[0].Field != "useGpu" {
		t.Errorf("disabled = %+v, want useGpu greyed without a card", check.Disabled)
	}

	// An out-of-range value is caught again even when it did not come through DecodeSettings.
	bad := videoSettings("hevc", 9)
	check = VideoStep{}.Validate(bad, flow.ValidateContext{Env: flow.Env{Encoders: []string{"libx265"}}})
	if len(check.Problems) != 1 || check.Problems[0].Code != "setting.range" {
		t.Errorf("problems = %+v, want the range", check.Problems)
	}
	_ = def
}

func TestVideoDefaults(t *testing.T) {
	s, err := flow.DecodeSettings(VideoStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := s.(VideoSettings)
	if got.Format != "hevc" || got.Quality != 3 || !got.UseGpu || got.IfGpuFails != "cpu" {
		t.Errorf("defaults = %+v", got)
	}
}

// TestVideoRefusesBadAdvancedSettingsAtRun is TagProblems on the advanced fields: the same rules
// DecodeSettings applies are checked again at run time, because an old flow may be out of range.
func TestVideoRefusesBadAdvancedSettingsAtRun(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.BitDepth = ptr(9)
	set.AvgMbps = ptr(0.05)
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	var fields []string
	for _, p := range check.Problems {
		fields = append(fields, p.Field+":"+p.Code)
	}
	for _, want := range []string{"bitDepth:setting.choice", "avgMbps:setting.range"} {
		if !slices.Contains(fields, want) {
			t.Errorf("problems = %v, want %s", fields, want)
		}
	}
}

// ladderPlan is the plan a rate-control setting makes, with the scale the flow would write.
func ladderPlan(t *testing.T, set VideoSettings, info media.Info, scale encode.Scale) encode.RateControl {
	t.Helper()
	p := encode.Plan{Video: encode.VideoPlan{Scale: scale}}
	if _, err := (VideoStep{}).Plan(set, flow.PlanInput{Info: info}, &p); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.RateControl == nil {
		t.Fatal("the plan has no rate control")
	}
	if p.Video.CRF != nil {
		t.Errorf("the plan kept CRF %d beside the bitrate", *p.Video.CRF)
	}
	return *p.RateControl
}

func almost(got, want float64) bool {
	d := got - want
	return d < 1e-9 && d > -1e-9
}

// TestVideoTakesTheLadderNumber: with a level, the engine takes the ladder's number for the output
// picture's row and format, raised for 60 fps and HDR files and never above the source's own rate.
func TestVideoTakesTheLadderNumber(t *testing.T) {
	vbr := videoSettings("hevc", 3)
	vbr.RateControl = ptr("vbr")
	vbr.UseGpu = false

	// 1080p, 30 fps, SDR, HEVC, Medium: the ladder's 1080/medium number, and the at-most is 1.5×.
	rate := ladderPlan(t, vbr, probeInfo("h264", 1920, 1080), encode.Scale{})
	if !almost(rate.AvgMbps, 3.6) || !almost(rate.MaxMbps, 5.4) {
		t.Errorf("1080p medium = %v/%v, want 3.6/5.4", rate.AvgMbps, rate.MaxMbps)
	}

	// The format's own column: H.264 at 1080p medium is 6.
	h264 := vbr
	h264.Format = "h264"
	if rate := ladderPlan(t, h264, probeInfo("hevc", 1920, 1080), encode.Scale{}); !almost(rate.AvgMbps, 6) {
		t.Errorf("h264 1080p medium = %v, want 6", rate.AvgMbps)
	}

	// The level picks the column; Low and High are the ladder's own numbers.
	low := vbr
	low.BitrateLevel = "low"
	if rate := ladderPlan(t, low, probeInfo("h264", 1920, 1080), encode.Scale{}); !almost(rate.AvgMbps, 1.8) {
		t.Errorf("1080p low = %v, want 1.8", rate.AvgMbps)
	}
	high := vbr
	high.BitrateLevel = "high"
	if rate := ladderPlan(t, high, probeInfo("h264", 1920, 1080), encode.Scale{}); !almost(rate.AvgMbps, 5.4) {
		t.Errorf("1080p high = %v, want 5.4", rate.AvgMbps)
	}

	// 60 fps raises the number by the ladder's factor.
	fast := probeInfo("h264", 1920, 1080)
	fast.Video[0].FrameRate = media.Rational{Num: 60, Den: 1}
	if rate := ladderPlan(t, vbr, fast, encode.Scale{}); !almost(rate.AvgMbps, 3.6*1.4) {
		t.Errorf("60 fps = %v, want %v", rate.AvgMbps, 3.6*1.4)
	}

	// HDR raises it too, and the two factors multiply.
	hdr := probeInfo("h264", 1920, 1080)
	hdr.Video[0].BitDepth = 10
	hdr.Video[0].ColorTransfer = "smpte2084"
	if rate := ladderPlan(t, vbr, hdr, encode.Scale{}); !almost(rate.AvgMbps, 3.6*1.25) {
		t.Errorf("HDR = %v, want %v", rate.AvgMbps, 3.6*1.25)
	}
	hdr.Video[0].FrameRate = media.Rational{Num: 60, Den: 1}
	if rate := ladderPlan(t, vbr, hdr, encode.Scale{}); !almost(rate.AvgMbps, 3.6*1.4*1.25) {
		t.Errorf("60 fps HDR = %v, want %v", rate.AvgMbps, 3.6*1.4*1.25)
	}
}

// TestVideoUsesTheOutputRowsNumber: a 4K source scaled to 1080 is a 1080 file, so it reads the 1080
// row rather than the 4K one. Without a scale it reads its own row.
func TestVideoUsesTheOutputRowsNumber(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.RateControl = ptr("cbr")
	set.BitrateLevel = "high"
	uhd := probeInfo("h264", 3840, 2160)

	if rate := ladderPlan(t, set, uhd, encode.Scale{}); !almost(rate.AvgMbps, 18) {
		t.Errorf("4K high without a scale = %v, want 18 (the 2160 row)", rate.AvgMbps)
	}
	if rate := ladderPlan(t, set, uhd, encode.Scale{Height: 1080}); !almost(rate.AvgMbps, 5.4) {
		t.Errorf("4K high scaled to 1080 = %v, want 5.4 (the 1080 row)", rate.AvgMbps)
	}
	// A rotated 4K phone clip is upright 2160x3840; the scale box fits its upright height.
	portrait := probeInfo("h264", 3840, 2160)
	portrait.Video[0].Rotation = 90
	if rate := ladderPlan(t, set, portrait, encode.Scale{Height: 1080}); !almost(rate.AvgMbps, 5.4) {
		t.Errorf("rotated 4K scaled to 1080 = %v, want 5.4", rate.AvgMbps)
	}
}

// TestVideoNeverAsksForMoreThanTheSource: the ladder's number is a ceiling under the source's own
// rate, because spending more than the original would gain nothing.
func TestVideoNeverAsksForMoreThanTheSource(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.RateControl = ptr("vbr")
	set.BitrateLevel = "high"
	info := probeInfo("h264", 1920, 1080)
	info.Video[0].BitRate = 2_000_000 // 2 Mb/s

	rate := ladderPlan(t, set, info, encode.Scale{})
	if !almost(rate.AvgMbps, 2) || !almost(rate.MaxMbps, 2) {
		t.Errorf("capped = %v/%v, want 2/2 (the source's rate)", rate.AvgMbps, rate.MaxMbps)
	}

	// A container that does not name the stream's rate still gives the file's overall rate.
	info.Video[0].BitRate = 0
	info.Format.BitRate = 3_000_000
	if rate := ladderPlan(t, set, info, encode.Scale{}); !almost(rate.AvgMbps, 3) {
		t.Errorf("format rate cap = %v, want 3", rate.AvgMbps)
	}
}

// TestVideoCustomUsesTheTypedNumbers: Custom takes the person's average and at-most as typed, with
// no ladder lookup and no source cap; CBR uses only the average.
func TestVideoCustomUsesTheTypedNumbers(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.RateControl = ptr("vbr")
	set.BitrateLevel = "custom"
	set.AvgMbps = ptr(4.2)
	set.MaxMbps = ptr(7.5)
	info := probeInfo("h264", 1920, 1080)
	info.Video[0].BitRate = 2_000_000 // a typed number is not capped by the source

	rate := ladderPlan(t, set, info, encode.Scale{})
	if !almost(rate.AvgMbps, 4.2) || !almost(rate.MaxMbps, 7.5) {
		t.Errorf("custom vbr = %v/%v, want 4.2/7.5", rate.AvgMbps, rate.MaxMbps)
	}

	// A custom VBR with no typed at-most takes the ladder's factor.
	set.MaxMbps = nil
	if rate := ladderPlan(t, set, info, encode.Scale{}); !almost(rate.MaxMbps, 4.2*1.5) {
		t.Errorf("custom vbr without an at-most = %v, want %v", rate.MaxMbps, 4.2*1.5)
	}

	// Custom CBR uses the average alone; the at-most is the same rate.
	cbr := set
	cbr.RateControl = ptr("cbr")
	cbr.MaxMbps = ptr(9.0)
	if rate := ladderPlan(t, cbr, info, encode.Scale{}); !almost(rate.AvgMbps, 4.2) || rate.MaxMbps != 0 {
		t.Errorf("custom cbr = %v/%v, want 4.2 with no separate at-most", rate.AvgMbps, rate.MaxMbps)
	}
}

// TestVideoCustomWithoutAnAverageIsAnError: there is no number to encode at, and guessing one would
// ignore what the person asked for.
func TestVideoCustomWithoutAnAverageIsAnError(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.RateControl = ptr("cbr")
	set.BitrateLevel = "custom"
	var p encode.Plan
	_, err := VideoStep{}.Plan(set, flow.PlanInput{Info: probeInfo("h264", 1920, 1080)}, &p)
	var pe *flow.PlanError
	if !errors.As(err, &pe) || pe.Code != "video.no_bitrate" {
		t.Errorf("error = %v, want video.no_bitrate", err)
	}
}

// TestVideoGreysVBRForAFormatWithoutIt: AV1 has no variable bitrate, so the check greys that choice
// of an AV1 flow with the page's sentence.
func TestVideoGreysVBRForAFormatWithoutIt(t *testing.T) {
	set := videoSettings("av1", 3)
	set.RateControl = ptr("vbr")
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	want := "Variable bitrate doesn't work with AV1 yet. Pick Constant bitrate or Quality, or another format."
	found := false
	for _, d := range check.Disabled {
		if d.Field == "rateControl" && d.Value == "vbr" && d.Why == want {
			found = true
		}
	}
	if !found {
		t.Errorf("disabled = %+v, want rateControl/vbr greyed with %q", check.Disabled, want)
	}

	// HEVC and H.264 do take it, so nothing is greyed for either.
	for _, format := range []string{"hevc", "h264"} {
		ok := videoSettings(format, 3)
		ok.RateControl = ptr("vbr")
		for _, d := range (VideoStep{}).Validate(ok, flow.ValidateContext{}).Disabled {
			if d.Field == "rateControl" {
				t.Errorf("%s: disabled = %+v, want VBR offered", format, d)
			}
		}
	}
}

// TestVideoNotYet pins which advanced settings are accepted now and reported "not yet": one
// problem per field, on the field that asked for it, until item 8 builds them. The bitrate and its
// level are built (item 6); two passes are not.
func TestVideoNotYet(t *testing.T) {
	set := videoSettings("hevc", 3)
	set.Size = ptr("1080")
	set.Preset = ptr("slower")
	set.FPS = ptr(30.0)
	set.BitDepth = ptr(10)
	set.HDR = ptr("tonemap")
	set.KeepDolbyVision = true
	set.RateControl = ptr("vbr")
	set.BitrateLevel = "high"
	set.AvgMbps = ptr(3.6)
	set.TwoPass = true
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	got := map[string]bool{}
	for _, p := range check.Problems {
		if p.Code != "video.not_yet" {
			t.Errorf("problem = %+v, want video.not_yet", p)
		}
		got[p.Field] = true
	}
	for _, field := range []string{"twoPass"} {
		if !got[field] {
			t.Errorf("problems = %v, want one on %s", got, field)
		}
	}
	// A bitrate and its level are acted on now; they are not reported.
	for _, field := range []string{"rateControl", "bitrateLevel", "avgMbps", "maxMbps"} {
		if got[field] {
			t.Errorf("problems = %v, want no problem on %s", got, field)
		}
	}
	// A bitrate greys the slider, and the summary says which bitrate instead of the quality.
	disabled := false
	for _, d := range check.Disabled {
		if d.Field == "quality" && strings.Contains(d.Why, "bitrate") {
			disabled = true
		}
	}
	if !disabled {
		t.Errorf("disabled = %+v, want the Quality slider greyed", check.Disabled)
	}
	// The card's line names the bitrate, not the quality.
	if !strings.HasPrefix(check.Summary, "HEVC · variable bitrate") || strings.Contains(check.Summary, "quality") {
		t.Errorf("summary = %q, want the bitrate in place of the quality", check.Summary)
	}

	// The defaults are the engine's current behaviour: nothing to report.
	def, err := flow.DecodeSettings(VideoStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if problems := (VideoStep{}).Validate(def, flow.ValidateContext{}).Problems; len(problems) != 0 {
		t.Errorf("defaults problems = %+v, want none", problems)
	}
}

// TestVideoDolbyVisionNeedsHEVC is page 07's greyed check box with its sentence.
func TestVideoDolbyVisionNeedsHEVC(t *testing.T) {
	set := videoSettings("h264", 3)
	set.KeepDolbyVision = true
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	found := false
	for _, d := range check.Disabled {
		if d.Field == "keepDolbyVision" && d.Why == "Only with HEVC." {
			found = true
		}
	}
	if !found {
		t.Errorf("disabled = %+v, want the Dolby Vision greyed on H.264", check.Disabled)
	}
}
