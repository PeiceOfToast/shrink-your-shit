// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"strings"
	"testing"
)

// The test encode and the test decode, exact, per family and depth. These are the arguments the hardware check
// runs; the encoder profiles (slice 5a) must leave them unchanged.
func TestEncodeAndDecodeArgsGoldens(t *testing.T) {
	const dev = "/dev/dri/renderD129"
	const head = "-hide_banner -v error"
	const src = " -f lavfi -i color=c=black:s=256x256:d=0.1 -frames:v 1"
	vaapiInit := " -init_hw_device vaapi=va:" + dev + " -filter_hw_device va"
	qsvInit := " -init_hw_device qsv=qsv:hw"
	cases := []struct {
		enc    string
		tenBit bool
		want   string
	}{
		{"hevc_vaapi", false, head + vaapiInit + src + " -vf format=nv12,hwupload -c:v hevc_vaapi -f null -"},
		{"hevc_vaapi", true, head + vaapiInit + src + " -vf format=p010,hwupload -c:v hevc_vaapi -profile:v main10 -f null -"},
		{"av1_vaapi", false, head + vaapiInit + src + " -vf format=nv12,hwupload -c:v av1_vaapi -f null -"},
		{"av1_vaapi", true, head + vaapiInit + src + " -vf format=p010,hwupload -c:v av1_vaapi -f null -"},
		{"hevc_qsv", false, head + qsvInit + src + " -c:v hevc_qsv -f null -"},
		{"hevc_qsv", true, head + qsvInit + src + " -pix_fmt p010le -c:v hevc_qsv -profile:v main10 -f null -"},
		{"h264_qsv", false, head + qsvInit + src + " -c:v h264_qsv -f null -"},
		{"av1_qsv", true, head + qsvInit + src + " -pix_fmt p010le -c:v av1_qsv -f null -"},
		{"hevc_nvenc", false, head + src + " -c:v hevc_nvenc -f null -"},
		{"hevc_nvenc", true, head + src + " -pix_fmt p010le -c:v hevc_nvenc -profile:v main10 -f null -"},
		{"av1_nvenc", true, head + src + " -pix_fmt p010le -c:v av1_nvenc -f null -"},
		{"hevc_amf", false, head + src + " -c:v hevc_amf -f null -"},
		{"hevc_amf", true, head + src + " -pix_fmt p010le -c:v hevc_amf -profile:v main10 -f null -"},
	}
	for _, c := range cases {
		if got := strings.Join(encodeArgs(c.enc, c.tenBit, dev), " "); got != c.want {
			t.Errorf("encodeArgs(%s, 10-bit=%v):\n got %s\nwant %s", c.enc, c.tenBit, got, c.want)
		}
	}
	if got, want := strings.Join(decodeArgs("vaapi", dev, "in.mp4"), " "),
		head+vaapiInit+" -hwaccel vaapi -hwaccel_output_format vaapi -hwaccel_device va -i in.mp4 -f null -"; got != want {
		t.Errorf("decodeArgs(vaapi):\n got %s\nwant %s", got, want)
	}
	if got, want := strings.Join(decodeArgs("qsv", "", "in.mp4"), " "),
		head+qsvInit+" -hwaccel qsv -hwaccel_output_format qsv -hwaccel_device qsv -i in.mp4 -f null -"; got != want {
		t.Errorf("decodeArgs(qsv):\n got %s\nwant %s", got, want)
	}
}
