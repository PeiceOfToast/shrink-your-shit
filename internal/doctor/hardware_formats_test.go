// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The encoder test covers H.264 and AV1 per graphics family, in hardwareEncoders' order, and runs the
// 10-bit check only for HEVC and AV1: an H.264 encoder never gets a Main10 or p010 test.
func TestHardwareTestCoversH264AndAV1(t *testing.T) {
	var seen []string
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		all := strings.Join(args, " ")
		switch {
		case strings.Contains(all, "-encoders"):
			// Listed out of order on purpose: the test follows hardwareEncoders, not this listing.
			return "ffmpeg version 7.1\n V..... av1_nvenc x\n V..... hevc_amf x\n V..... h264_amf x\n" +
				" V..... h264_nvenc x\n V..... hevc_nvenc x\n V..... hevc_videotoolbox x\n", "", nil
		case strings.Contains(all, "-c:v"):
			seen = append(seen, all)
			if strings.Contains(all, "-c:v h264_amf") {
				return "", "", errors.New("h264_amf not supported")
			}
			return "", "", nil
		}
		return "ffmpeg version 7.1\n", "", nil
	}}
	c := &Checker{Exec: x, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"}
	tools := c.Tools(context.Background())

	if got := strings.Join(tools.Hardware, ","); got != "hevc_nvenc,h264_nvenc,av1_nvenc,hevc_amf,hevc_videotoolbox" {
		t.Errorf("passed encoders %q, want the hardwareEncoders order", got)
	}
	if got := strings.Join(tools.HardwareListed, ","); got != "h264_amf" {
		t.Errorf("failed encoders %q, want h264_amf", got)
	}
	if got := strings.Join(tools.Hardware10, ","); got != "hevc_nvenc,av1_nvenc,hevc_amf,hevc_videotoolbox" {
		t.Errorf("10-bit encoders %q, want HEVC and AV1 only", got)
	}
	for _, call := range seen {
		if strings.Contains(call, "-c:v h264_") && strings.Contains(call, "p010le") {
			t.Errorf("an H.264 encoder got a 10-bit test: %s", call)
		}
		if strings.Contains(call, "-c:v av1_") && strings.Contains(call, "main10") {
			t.Errorf("an AV1 encoder got the HEVC Main10 profile: %s", call)
		}
	}
}
