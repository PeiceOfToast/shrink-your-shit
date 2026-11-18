// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"strings"
	"testing"
)

// gpuPlan is a libx265 Plan at crf, with the given extras on its video encode.
func gpuPlan(crf int, edit func(*VideoEncode)) Plan {
	v := VideoEncode{Codec: "libx265", CRF: crf, Preset: "medium"}
	if edit != nil {
		edit(&v)
	}
	return Plan{Video: v}
}

// has reports whether args holds the run of values in order, somewhere.
func has(args []string, run ...string) bool {
	for i := 0; i+len(run) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

func TestFFmpegArgsWithCRFPerEncoder(t *testing.T) {
	cases := []struct {
		enc  string
		want []string
	}{
		{"hevc_nvenc", []string{"-c:v:0", "hevc_nvenc", "-rc", "vbr", "-cq", "22"}},
		{"hevc_qsv", []string{"-c:v:0", "hevc_qsv", "-global_quality", "22"}},
		{"hevc_vaapi", []string{"-c:v:0", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "22"}},
		{"hevc_amf", []string{"-c:v:0", "hevc_amf", "-rc", "cqp", "-qp_i", "22", "-qp_p", "22"}},
	}
	for _, c := range cases {
		args, ok := gpuPlan(22, nil).FFmpegArgsWith("in.mkv", "out.mkv", c.enc)
		if !ok || !has(args, c.want...) {
			t.Errorf("%s: ok %v, want %v in %v", c.enc, ok, c.want, args)
		}
		for _, bad := range []string{"-crf", "-preset", "-x265-params:0", "-b:v:0"} {
			if hasFlag(args, bad) {
				t.Errorf("%s: %s must not be there: %v", c.enc, bad, args)
			}
		}
	}
}

func TestFFmpegArgsWithVBRKeepsBitrate(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.TargetKbps = 5000 })
	for _, enc := range []string{"hevc_nvenc", "hevc_qsv", "hevc_vaapi", "hevc_amf"} {
		args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || !has(args, "-c:v:0", enc, "-b:v:0", "5000k") || hasFlag(args, "-cq") || hasFlag(args, "-global_quality") || hasFlag(args, "-qp") {
			t.Errorf("%s: ok %v, args %v", enc, ok, args)
		}
	}
}

func TestFFmpegArgsWithHDR(t *testing.T) {
	hdr := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	p := gpuPlan(22, func(v *VideoEncode) { v.HDR = hdr })
	for _, enc := range []string{"hevc_nvenc", "hevc_qsv", "hevc_amf"} {
		args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || !has(args, "-profile:v:0", "main10") || !has(args, "-pix_fmt:v:0", "p010le") ||
			!has(args, "-color_trc:v:0", "smpte2084") || !has(args, "-colorspace:v:0", "bt2020nc") || hasFlag(args, "-x265-params:v:0") {
			t.Errorf("%s: ok %v, args %v", enc, ok, args)
		}
	}
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || hasFlag(args, "-pix_fmt:v:0") || !has(args, "-filter:v:0", "format=p010,hwupload") {
		t.Errorf("vaapi HDR: ok %v, args %v", ok, args)
	}
}

func TestFFmpegArgsWithScaleAndVAAPIFilterOrder(t *testing.T) {
	old := vaapiDevice
	vaapiDevice = func() string { return "/dev/dri/renderD129" }
	defer func() { vaapiDevice = old }()

	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 1080 })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-filter:v:0", "scale=-2:1080,format=nv12,hwupload") {
		t.Errorf("vaapi scale then upload: ok %v, args %v", ok, args)
	}
	if !has(args, "-init_hw_device", "vaapi=va:/dev/dri/renderD129", "-filter_hw_device", "va") {
		t.Errorf("vaapi device: %v", args)
	}
	in := indexOf(args, "-i")
	if in < 0 || indexOf(args, "-init_hw_device") > in {
		t.Errorf("vaapi device must come before -i: %v", args)
	}

	nv, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_nvenc")
	if !ok || !has(nv, "-filter:v:0", "scale=-2:1080") || contains(nv, "hwupload") {
		t.Errorf("nvenc scale: ok %v, args %v", ok, nv)
	}
	qsv, _ := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_qsv")
	if !has(qsv, "-init_hw_device", "qsv=qsv:hw") || indexOf(qsv, "-init_hw_device") > indexOf(qsv, "-i") {
		t.Errorf("qsv device before -i: %v", qsv)
	}
}

func TestFFmpegArgsWithCPUIsFFmpegArgs(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 720 })
	want := p.FFmpegArgs("in.mkv", "out.mkv")
	for _, enc := range []string{"", "cpu"} {
		got, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: ok %v\n got %v\nwant %v", enc, ok, got, want)
		}
	}
}

func TestFFmpegArgsWithFallsBackToCPU(t *testing.T) {
	x265 := gpuPlan(22, nil)
	want := x265.FFmpegArgs("in.mkv", "out.mkv")
	cases := []struct {
		name string
		p    Plan
		enc  string
	}{
		{"unknown encoder", x265, "hevc_videotoolbox"},
		{"copy", Plan{Video: VideoEncode{Codec: "copy", Copy: true}}, "hevc_nvenc"},
		{"not libx265", Plan{Video: VideoEncode{Codec: "libx264", CRF: 22}}, "hevc_nvenc"},
	}
	for _, c := range cases {
		got, ok := c.p.FFmpegArgsWith("in.mkv", "out.mkv", c.enc)
		if ok {
			t.Errorf("%s: ok true", c.name)
		}
		if !reflect.DeepEqual(got, c.p.FFmpegArgs("in.mkv", "out.mkv")) {
			t.Errorf("%s: not the processor's args: %v", c.name, got)
		}
	}
	if got, _ := x265.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_videotoolbox"); !reflect.DeepEqual(got, want) {
		t.Errorf("unknown encoder: %v", got)
	}
}

func hasFlag(args []string, s string) bool {
	for _, a := range args {
		if a == s || strings.HasPrefix(a, s+"=") {
			return true
		}
	}
	return false
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}
