// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// requireRealTools is the media package's L-INST-7 rule: skip only when a tool is missing and
// SYP_REQUIRE_FFMPEG is unset; CI sets it and also fails on any "--- SKIP: TestReal".
func requireRealTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s is not on PATH and SYP_REQUIRE_FFMPEG is set: the real encode test must not skip", tool)
			}
			t.Skipf("%s is not on PATH (set SYP_REQUIRE_FFMPEG=1 to fail instead)", tool)
		}
	}
}

// listedEncoders is the encoders this ffmpeg build offers, by name.
func listedEncoders(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatalf("ffmpeg -encoders: %v", err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "V") {
			names[fields[1]] = true
		}
	}
	return names
}

// TestRealEncodesEachCodec runs each processor encoder through media.Run with the built arguments
// and probes the output: the codec, tag, bit depth and container are what the plan asked for.
func TestRealEncodesEachCodec(t *testing.T) {
	requireRealTools(t)
	available := listedEncoders(t)
	dir := t.TempDir()

	source := filepath.Join(dir, "source.mp4")
	make := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", source)
	if out, err := make.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	info, err := media.Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}

	codecs := []struct {
		name  string
		codec Codec
		tag   string
	}{
		{"hevc", CodecHEVC, "hvc1"},
		{"h264", CodecH264, "avc1"},
		{"av1", CodecAV1, "av01"},
	}
	for _, c := range codecs {
		t.Run(c.name, func(t *testing.T) {
			encoder, _ := CPUEncoder(c.codec)
			if !available[encoder] {
				if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
					t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
				}
				t.Skipf("this ffmpeg build has no %s", encoder)
			}

			out := filepath.Join(dir, "out-"+c.name+".mp4")
			plan := Plan{
				Video:     VideoPlan{Codec: c.codec, CRF: intp(32), Preset: "faster"},
				Container: ContainerMP4,
				Output:    out,
			}
			res, err := Build(plan, info, cpuRung(c.codec))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			argv := append([]string{"ffmpeg"}, res.Argv...)
			run, err := media.Run(context.Background(), argv, nil)
			if err != nil {
				t.Fatalf("ffmpeg: %v (exit %d)\n%s", err, run.ExitCode, strings.Join(run.KeyLines, "\n"))
			}

			got, err := media.Probe(context.Background(), out)
			if err != nil {
				t.Fatalf("probe the output: %v", err)
			}
			if len(got.Video) != 1 {
				t.Fatalf("video tracks = %d, want 1", len(got.Video))
			}
			v := got.Video[0]
			if v.Codec != string(c.codec) {
				t.Errorf("codec = %q, want %q", v.Codec, c.codec)
			}
			if v.Tag != c.tag {
				t.Errorf("tag = %q, want %q", v.Tag, c.tag)
			}
			if v.BitDepth != 8 {
				t.Errorf("bit depth = %d, want 8 from the source", v.BitDepth)
			}
			if got.Format.Names[0] != "mov" {
				t.Errorf("container = %q, want mov (mp4)", got.Format.Names[0])
			}
			if d := got.Format.Duration; d < 1500*time.Millisecond || d > 2500*time.Millisecond {
				t.Errorf("duration = %v, want about 2 s", d)
			}
			t.Logf("%s: codec=%s tag=%s pix_fmt=%s container=%s duration=%v",
				c.name, v.Codec, v.Tag, v.PixelFormat, got.Format.Names[0], got.Format.Duration)
		})
	}
}
