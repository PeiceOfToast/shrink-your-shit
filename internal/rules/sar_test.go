// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"
)

// compiledVideo is the plan a 1080-line source gets at the 480 cap, for a source of the given pixel shape.
func compiledVideo(t *testing.T, w, h int, square bool) Plan {
	t.Helper()
	src := h264(w, h, 3000)
	src.Streams[0].SquarePixels = square
	s := DefaultSelectors(3)
	s.Resolution = "480"
	return s.Template().Compile(src)
}

// A square source's scaled picture is set to 1:1: the scale alone rounds the width and leaves SAR 1280:1281.
func TestSquareSourceScaleSetsTheSAR(t *testing.T) {
	p := compiledVideo(t, 1920, 1080, true)
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if !p.Video.SquarePixels || !strings.Contains(args, "-filter:v:0 scale=-2:480,setsar=1") {
		t.Errorf("square source: SquarePixels %v, args %s", p.Video.SquarePixels, args)
	}
}

// An anamorphic source (1440x1080 at SAR 4:3, a 16:9 picture) keeps its SAR through the scale: setsar=1 would
// squash it to 4:3, which is the bug this change avoids.
func TestAnamorphicSourceKeepsItsSAR(t *testing.T) {
	p := compiledVideo(t, 1440, 1080, false)
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if p.Video.SquarePixels || !strings.Contains(args, "-filter:v:0 scale=-2:480") || strings.Contains(args, "setsar") {
		t.Errorf("anamorphic source: SquarePixels %v, args %s", p.Video.SquarePixels, args)
	}
}

// A square source that is not scaled has no scale to round, so it gets no setsar either.
func TestUnscaledSquareSourceHasNoSetsar(t *testing.T) {
	p := compiledVideo(t, 640, 360, true)
	if p.Video.Height != 0 {
		t.Fatalf("640x360 should not scale to the 480 cap, got height %d", p.Video.Height)
	}
	if args := strings.Join(p.FFmpegArgs("i", "o"), " "); strings.Contains(args, "setsar") {
		t.Errorf("unscaled square source: args %s", args)
	}
}

// On the hybrid path the software scale is followed by setsar, before the upload to the device.
func TestHybridSquareSourceSetsTheSARBeforeUpload(t *testing.T) {
	old := vaapiDevice
	vaapiDevice = func() string { return "/dev/dri/renderD129" }
	defer func() { vaapiDevice = old }()

	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 480; v.SquarePixels = true })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-filter:v:0", "scale=-2:480,setsar=1,format=nv12,hwupload") {
		t.Errorf("vaapi hybrid square: ok %v, args %v", ok, args)
	}
	anam, _ := gpuPlan(22, func(v *VideoEncode) { v.Height = 480 }).FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !has(anam, "-filter:v:0", "scale=-2:480,format=nv12,hwupload") {
		t.Errorf("vaapi hybrid anamorphic must not change: args %v", anam)
	}
}

// The full graphics pipeline's scale is the device's own filter; it is left as it was, and every width still comes
// from the same rule (ScaledWidth).
func TestFullPipelineScaleIsUnchanged(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 480; v.SquarePixels = true })
	args, ok := p.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_qsv")
	if !ok {
		t.Fatalf("qsv full pipeline not built: %v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "scale_qsv=w=trunc(480*iw/ih/2+0.5)*2:h=480") || strings.Contains(joined, "setsar") {
		t.Errorf("qsv full pipeline: %s", joined)
	}
}
