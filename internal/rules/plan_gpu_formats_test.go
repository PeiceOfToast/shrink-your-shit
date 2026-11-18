// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"strings"
	"testing"
)

// formatPlan is a Plan of codec at crf, the way a Standard H.264 or AV1 conversion is made.
func formatPlan(codec string, crf int, edit func(*VideoEncode)) Plan {
	v := VideoEncode{Codec: codec, CRF: crf, Preset: "medium"}
	if edit != nil {
		edit(&v)
	}
	return Plan{Video: v}
}

// An H.264 plan encodes on its family's H.264 encoder, with the family's device setup and quality options and
// none of libx264's own.
func TestH264PlanUsesTheGraphicsEncoder(t *testing.T) {
	p := formatPlan("libx264", 22, func(v *VideoEncode) { v.Tag = "avc1" })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "h264_qsv")
	if !ok {
		t.Fatalf("h264_qsv: ok false")
	}
	if !has(args, "-c:v:0", "h264_qsv") || !has(args, "-init_hw_device", "qsv=qsv:hw") || !has(args, "-global_quality", "22") {
		t.Errorf("args: %v", args)
	}
	if !has(args, "-tag:v:0", "avc1") {
		t.Errorf("output tag: %v", args)
	}
	for _, bad := range []string{"-preset", "-crf", "libx264", "-x264-params"} {
		if strings.Contains(strings.Join(args, " "), bad) {
			t.Errorf("%s reached the graphics encoder: %v", bad, args)
		}
	}
}

// An H.264 or AV1 plan whose format has no encoder in the family, or that is not a graphics encoder at all,
// is the processor's, exactly.
func TestFormatWithoutItsGraphicsEncoderIsTheProcessor(t *testing.T) {
	h264 := formatPlan("libx264", 22, nil)
	av1 := formatPlan("libsvtav1", 30, nil)
	for _, c := range []struct {
		name string
		p    Plan
		enc  string
	}{
		{"h264 plan, hevc encoder", h264, "hevc_qsv"},
		{"av1 plan, h264 encoder", av1, "h264_qsv"},
		{"av1 plan, unknown family", av1, "av1_videotoolbox"},
		{"h264 plan, HDR", formatPlan("libx264", 22, func(v *VideoEncode) { v.HDR = &HDR{Primaries: "bt2020", Transfer: "smpte2084", Matrix: "bt2020nc"} }), "h264_qsv"},
	} {
		got, ok := c.p.FFmpegArgsWith("in.mkv", "out.mkv", c.enc)
		if ok {
			t.Errorf("%s: ok true", c.name)
		}
		if !reflect.DeepEqual(got, c.p.FFmpegArgs("in.mkv", "out.mkv")) {
			t.Errorf("%s: not the processor's args: %v", c.name, got)
		}
	}
}

// An AV1 plan's SVT-AV1 parameters don't reach its graphics encoder.
func TestAV1PlanDropsSVTParamsOnTheGraphicsEncoder(t *testing.T) {
	p := formatPlan("libsvtav1", 30, func(v *VideoEncode) { v.Extra = []string{"-svtav1-params", "tune=0"}; v.Preset = "" })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "av1_qsv")
	if !ok {
		t.Fatalf("av1_qsv: ok false")
	}
	if !has(args, "-c:v:0", "av1_qsv") || !has(args, "-global_quality", "30") {
		t.Errorf("args: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "svtav1") {
		t.Errorf("SVT-AV1 parameters reached the graphics encoder: %v", args)
	}
}

// The family of a graphics encoder, and the encoder of another format in that family.
func TestGraphicsEncoderForAFormat(t *testing.T) {
	if got := GraphicsEncoder("libx264", "hevc_qsv"); got != "h264_qsv" {
		t.Errorf("H.264 in qsv: %q", got)
	}
	if got := GraphicsEncoder("libsvtav1", "h264_nvenc"); got != "av1_nvenc" {
		t.Errorf("AV1 in nvenc: %q", got)
	}
	if got := GraphicsEncoder("libvpx-vp9", "hevc_qsv"); got != "" {
		t.Errorf("VP9 has no graphics encoder: %q", got)
	}
	if got := GraphicsEncoder("libx265", "cpu"); got != "" {
		t.Errorf("a processor name has no family: %q", got)
	}
}
