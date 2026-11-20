// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"strings"
	"testing"
)

// fullArgs is the full graphics pipeline's arguments, one string, for a golden.
func fullArgs(t *testing.T, p Plan, enc string) string {
	t.Helper()
	args, ok := p.FFmpegArgsFullGPU("in.mkv", "out.mkv", enc)
	if !ok {
		t.Fatalf("%s: ok false, args %v", enc, args)
	}
	return strings.Join(args, " ")
}

// The full pipeline for QSV and VAAPI: the device decodes, the frames stay on it, and a scale is the family's own
// filter with no upload. Goldens per family x {no scale, downscale, HDR, HDR downscale}.
func TestFFmpegArgsFullGPUGoldens(t *testing.T) {
	old := vaapiDevice
	vaapiDevice = func() string { return "/dev/dri/renderD129" }
	defer func() { vaapiDevice = old }()

	hdr := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	const head = "-y -nostdin -v error -progress pipe:1 -nostats "
	const tail = " -map 0 -map -0:d -c copy -max_muxing_queue_size 4096 "
	qsvDev := "-init_hw_device qsv=qsv:hw -hwaccel qsv -hwaccel_output_format qsv -hwaccel_device qsv -i in.mkv"
	vaDev := "-init_hw_device vaapi=va:/dev/dri/renderD129 -filter_hw_device va -hwaccel vaapi -hwaccel_output_format vaapi -hwaccel_device va -i in.mkv"
	hdrFlags := " -profile:v:0 main10 -color_primaries:v:0 bt2020 -color_trc:v:0 smpte2084 -colorspace:v:0 bt2020nc"

	cases := []struct {
		name string
		p    Plan
		enc  string
		want string
	}{
		{"qsv plain", gpuPlan(22, nil), "hevc_qsv",
			head + qsvDev + tail + "-c:v:0 hevc_qsv -global_quality 22 out.mkv"},
		{"qsv downscale", gpuPlan(22, func(v *VideoEncode) { v.Height = 1080 }), "hevc_qsv",
			head + qsvDev + tail + "-c:v:0 hevc_qsv -global_quality 22 -filter:v:0 scale_qsv=w=trunc(1080*a/2)*2:h=1080 out.mkv"},
		{"qsv HDR", gpuPlan(22, func(v *VideoEncode) { v.HDR = hdr }), "hevc_qsv",
			head + qsvDev + tail + "-c:v:0 hevc_qsv -global_quality 22" + hdrFlags + " out.mkv"},
		{"qsv HDR downscale", gpuPlan(22, func(v *VideoEncode) { v.HDR = hdr; v.Height = 1080 }), "hevc_qsv",
			head + qsvDev + tail + "-c:v:0 hevc_qsv -global_quality 22" + hdrFlags + " -filter:v:0 scale_qsv=w=trunc(1080*a/2)*2:h=1080 out.mkv"},
		{"vaapi plain", gpuPlan(22, nil), "hevc_vaapi",
			head + vaDev + tail + "-c:v:0 hevc_vaapi -rc_mode CQP -qp 22 out.mkv"},
		{"vaapi downscale", gpuPlan(22, func(v *VideoEncode) { v.Height = 1080 }), "hevc_vaapi",
			head + vaDev + tail + "-c:v:0 hevc_vaapi -rc_mode CQP -qp 22 -filter:v:0 scale_vaapi=w=-2:h=1080:format=nv12 out.mkv"},
		{"vaapi HDR", gpuPlan(22, func(v *VideoEncode) { v.HDR = hdr }), "hevc_vaapi",
			head + vaDev + tail + "-c:v:0 hevc_vaapi -rc_mode CQP -qp 22" + hdrFlags + " out.mkv"},
		{"vaapi HDR downscale", gpuPlan(22, func(v *VideoEncode) { v.HDR = hdr; v.Height = 1080 }), "hevc_vaapi",
			head + vaDev + tail + "-c:v:0 hevc_vaapi -rc_mode CQP -qp 22" + hdrFlags + " -filter:v:0 scale_vaapi=w=-2:h=1080:format=p010 out.mkv"},
	}
	for _, c := range cases {
		if got := fullArgs(t, c.p, c.enc); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// No upload and no software frames on the full path: the only filter is the family's own scale.
func TestFFmpegArgsFullGPUHasNoUpload(t *testing.T) {
	for _, enc := range []string{"hevc_qsv", "hevc_vaapi"} {
		args, _ := gpuPlan(22, func(v *VideoEncode) { v.Height = 720 }).FFmpegArgsFullGPU("in.mkv", "out.mkv", enc)
		if hasFlag(args, "-pix_fmt:v:0") || contains(args, "hwupload") || contains(args, "format=nv12,hwupload") {
			t.Errorf("%s: uploads or software frames: %v", enc, args)
		}
	}
}

// Only QSV and VAAPI have the full pipeline. Anything else gets FFmpegArgsWith's arguments and ok false.
func TestFFmpegArgsFullGPUOnlyForQSVAndVAAPI(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 1080 })
	for _, enc := range []string{"hevc_nvenc", "hevc_amf", "", "cpu", "hevc_videotoolbox"} {
		got, ok := p.FFmpegArgsFullGPU("in.mkv", "out.mkv", enc)
		want, _ := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: ok %v, args %v; want ok false and %v", enc, ok, got, want)
		}
	}
}

// A plan that can't use a graphics encoder (copy, H.264 HDR, unknown encoder) is not a full pipeline either.
func TestFFmpegArgsFullGPUFallsBackWhenFFmpegArgsWithDoes(t *testing.T) {
	hdr := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	cases := []struct {
		name string
		p    Plan
	}{
		{"copy", Plan{Video: VideoEncode{Codec: "copy", Copy: true}}},
		{"H.264 HDR", Plan{Video: VideoEncode{Codec: "libx264", CRF: 22, HDR: hdr}}},
	}
	for _, c := range cases {
		got, ok := c.p.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_qsv")
		if ok || !reflect.DeepEqual(got, c.p.FFmpegArgs("in.mkv", "out.mkv")) {
			t.Errorf("%s: ok %v, args %v", c.name, ok, got)
		}
	}
}

// The processor's arguments are unchanged by the full pipeline: this is FFmpegArgs for the common plan.
func TestFFmpegArgsProcessorGolden(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 720 })
	want := "-y -nostdin -v error -progress pipe:1 -nostats -i in.mkv -map 0 -map -0:d -c copy -max_muxing_queue_size 4096 " +
		"-c:v:0 libx265 -crf 22 -preset medium -filter:v:0 scale=-2:720 out.mkv"
	if got := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " "); got != want {
		t.Errorf("processor:\n got %s\nwant %s", got, want)
	}
}
