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

// TestVideoNotYet pins which advanced settings are accepted now and reported "not yet": one
// problem per field, on the field that asked for it, until items 6 and 8 build them.
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
	check := VideoStep{}.Validate(set, flow.ValidateContext{})
	got := map[string]bool{}
	for _, p := range check.Problems {
		if p.Code != "video.not_yet" {
			t.Errorf("problem = %+v, want video.not_yet", p)
		}
		got[p.Field] = true
	}
	for _, field := range []string{"size", "preset", "fps", "bitDepth", "hdr", "keepDolbyVision", "rateControl", "bitrateLevel"} {
		if !got[field] {
			t.Errorf("problems = %v, want one on %s", got, field)
		}
	}
	// The bitrate fields live inside the one rateControl problem; they are not repeated.
	if got["avgMbps"] {
		t.Errorf("problems = %v, want no separate avgMbps problem while rateControl is the one named", got)
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
	// The card's line names the bitrate, not the quality, and the level is not the default.
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
