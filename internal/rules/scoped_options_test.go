// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strconv"
	"strings"
	"testing"
)

// A10 (user bug): every encoder-wide quality/rate option names the video stream, so it can never reach the
// audio encoder. An unscoped -global_quality made libopus refuse the whole job ("Quality-based encoding not
// supported"), which the app then blamed on the graphics card.
func TestEveryEncoderOptionNamesTheVideoStream(t *testing.T) {
	p := LegacyTemplate(Default(), "replace").Compile(h264(1920, 1080, 8000))
	if p.Video.TargetKbps != 0 {
		t.Fatalf("the plan is VBR (%d kbps): its quality options are not in it", p.Video.TargetKbps)
	}
	// the options a family's profile or the processor's path sets: each on its own cannot reach another stream
	quality := map[string]bool{"-crf": true, "-preset": true, "-global_quality": true, "-rc": true, "-cq": true,
		"-qp": true, "-qp_i": true, "-qp_p": true, "-rc_mode": true}
	spec := ":v:" + strconv.Itoa(p.Video.Index)
	for _, enc := range []string{"", "hevc_qsv", "hevc_nvenc", "hevc_vaapi", "hevc_amf"} {
		args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", enc)
		if enc != "" && !ok {
			t.Fatalf("%s: the plan has no such encoder", enc)
		}
		seen := 0
		for _, a := range args {
			name := a
			if i := strings.IndexByte(a, ':'); i > 0 {
				name = a[:i]
			}
			if !quality[name] {
				continue
			}
			seen++
			if a != name+spec {
				t.Errorf("%s: %q names no video stream, so it reaches the audio encoder too:\n%s",
					orCPU(enc), a, strings.Join(args, " "))
			}
		}
		if seen == 0 {
			t.Errorf("%s: no quality option in the arguments", orCPU(enc))
		}
	}
}

func orCPU(enc string) string {
	if enc == "" {
		return "processor"
	}
	return enc
}
