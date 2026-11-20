// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// decodeArgs is the device setup of the full GPU pipeline, decoding only: the family's device, then -hwaccel.
func TestDecodeArgsSetUpTheFamilysDevice(t *testing.T) {
	got := strings.Join(decodeArgs("vaapi", "/dev/dri/renderD129", "s.mp4"), " ")
	want := "-hide_banner -v error -init_hw_device vaapi=va:/dev/dri/renderD129 -filter_hw_device va -hwaccel vaapi -hwaccel_output_format vaapi -hwaccel_device va -i s.mp4 -f null -"
	if got != want {
		t.Errorf("vaapi:\n%s\nwant\n%s", got, want)
	}
	got = strings.Join(decodeArgs("qsv", "", "s.mp4"), " ")
	if !strings.Contains(got, "-init_hw_device qsv=qsv:hw -hwaccel qsv -hwaccel_output_format qsv -hwaccel_device qsv -i s.mp4") {
		t.Errorf("qsv: %s", got)
	}
}

// Decodes probes once per family, codec and bit depth, and caches the answer; a codec it has no sample for, or a
// family that is not graphics, is never asked of ffmpeg.
func TestDecodesCachesAndSkipsWhatItCannotSample(t *testing.T) {
	failDecode := false
	var decodes int
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		if strings.Contains(strings.Join(args, " "), "-hwaccel") {
			decodes++
			if failDecode {
				return "", "decode failed", errors.New("exit status 1")
			}
		}
		return "", "", nil
	}}
	c := &Checker{Exec: x, FFmpeg: "/usr/bin/ffmpeg"}
	ctx := context.Background()
	if !c.Decodes(ctx, "qsv", "h264", false) {
		t.Fatal("qsv h264 should decode")
	}
	if !c.Decodes(ctx, "qsv", "h264", false) || decodes != 1 {
		t.Errorf("second ask should be cached: %d decode runs, want 1", decodes)
	}
	if c.Decodes(ctx, "qsv", "av1", false) || c.Decodes(ctx, "nvenc", "h264", false) || decodes != 1 {
		t.Errorf("av1 and nvenc must not be probed: %d decode runs", decodes)
	}
	failDecode = true
	if c.Decodes(ctx, "vaapi", "hevc", true) {
		t.Error("a failing decode must say no")
	}
	if decodes != 2 {
		t.Errorf("decode runs %d, want 2 (vaapi hevc 10-bit probed once)", decodes)
	}
}
