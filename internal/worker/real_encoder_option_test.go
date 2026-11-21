// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A10, on real ffmpeg: the QSV profile's plan with Opus audio gets past option parsing when its quality option
// names the video stream, and the same option unscoped is the user's failure (libopus refuses the whole job).
// No QSV is needed: the encoder is swapped for the software one of the same shape and the plan's own option
// shape is kept.
func TestRealScopedQualityOptionReachesOpusAudio(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mkv")
	// 5.1 audio, so the stereo_opus choice has to encode the audio: a source already in stereo would be copied
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2", "-ac", "6", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", in)

	rules.SetAvailableFormats(media.Encoders(context.Background(), media.OSExec{}, "ffmpeg"))
	defer rules.SetAvailableFormats(nil)
	s := rules.DefaultSelectors(3)
	s.Audio = "stereo_opus"
	if err := s.Validate(); err != nil {
		skipUnlessRequired(t, "the stereo_opus audio choice needs an encoder this ffmpeg lacks: %v", err)
	}
	p := s.Template().Compile(probe(t, in))
	if p.Skip != "" {
		t.Fatalf("the plan is skipped: %s", p.Skip)
	}
	out := filepath.Join(dir, "out.mkv")
	args, ok := p.FFmpegArgsWith(in, out, "hevc_qsv")
	if !ok {
		t.Fatal("the plan has no hevc_qsv encoder")
	}
	// the software encoder of the same shape, without the QSV device it would have opened: the option shape of
	// the plan is what this test is about
	var kept []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "hevc_qsv":
			kept = append(kept, "libx265")
		case a == "-init_hw_device" || a == "-hwaccel" || a == "-hwaccel_output_format" || a == "-hwaccel_device":
			i++ // its value goes with it
		default:
			kept = append(kept, a)
		}
	}
	args = kept
	quality := 0
	for _, a := range args {
		if strings.HasPrefix(a, "-global_quality:") {
			quality++
		}
	}
	audio := strings.Contains(strings.Join(args, " "), "libopus")
	if quality == 0 || !audio {
		t.Fatalf("the plan has %d quality options and Opus audio: %v; want both:\n%s",
			quality, audio, strings.Join(args, " "))
	}

	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("the scoped quality option reached the audio encoder: %v\n%s", err, output)
	}

	// the user's failure, for the same arguments with the option unscoped
	for i, a := range args {
		if strings.HasPrefix(a, "-global_quality:") {
			args[i] = "-global_quality"
			break
		}
	}
	outBytes, err := exec.Command("ffmpeg", args...).CombinedOutput()
	if err == nil {
		t.Fatalf("the unscoped quality option converted; the test cannot show the user's failure:\n%s", outBytes)
	}
	if !strings.Contains(strings.ToLower(string(outBytes)), "quality-based encoding not supported") {
		t.Errorf("the unscoped option failed with %v, not with libopus's refusal:\n%s", err, outBytes)
	}
}
