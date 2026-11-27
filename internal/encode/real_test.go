// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestRealScaleKeepsARotatedClipUpright is the phone-video case (the old app's #81): a 320×240 clip
// turned with a Display Matrix is shown as 240×320 upright, and a height-180 box must fit that
// upright picture. Before the rotation fact was read, the filter was given the coded size and the
// portrait came out squashed into 240×180.
func TestRealScaleKeepsARotatedClipUpright(t *testing.T) {
	requireRealTools(t)
	if !listedEncoders(t)["libx265"] {
		if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
			t.Fatalf("this ffmpeg build has no libx265 and SYP_REQUIRE_FFMPEG is set")
		}
		t.Skip("this ffmpeg build has no libx265")
	}
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.mp4")
	make := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", plain)
	if out, err := make.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	source := filepath.Join(dir, "rotated.mp4")
	turn := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-display_rotation", "90", "-i", plain, "-c", "copy", source)
	if out, err := turn.CombinedOutput(); err != nil {
		t.Fatalf("turn the clip: %v\n%s", err, out)
	}

	info, err := media.Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	if v := info.Video[0]; v.Width != 320 || v.Height != 240 || v.Rotation != 90 {
		t.Fatalf("source = %dx%d rotation %d, want 320x240 rotation 90", v.Width, v.Height, v.Rotation)
	}

	out := filepath.Join(dir, "out.mkv")
	plan := Plan{
		Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(32), Preset: "faster", Scale: Scale{Height: 180}},
		Container: ContainerMKV,
		Output:    out,
	}
	res, err := Build(plan, info, cpuRung(CodecHEVC))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	run, err := media.Run(context.Background(), append([]string{"ffmpeg"}, res.Argv...), nil)
	if err != nil {
		t.Fatalf("ffmpeg: %v (exit %d)\n%s", err, run.ExitCode, strings.Join(run.KeyLines, "\n"))
	}

	got, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	// The filter turns the picture itself, so the output is upright with no matrix left: 240×320
	// fitted to height 180 is 135 wide, rounded down to the even 134.
	v := got.Video[0]
	if v.Width != 134 || v.Height != 180 {
		t.Errorf("output = %dx%d, want the upright 134x180", v.Width, v.Height)
	}
	if v.Rotation != 0 {
		t.Errorf("output rotation = %d, want 0 (the turn is applied by the filter)", v.Rotation)
	}
}

// cbrSegments is the video's bitrate in each whole second, from the packet sizes ffprobe reports,
// so a test can see whether rate control keeps the rate second by second or only averages to it.
// The last, partial second is not trimmed here; the caller picks the whole seconds it measured.
func cbrSegments(t *testing.T, path string) []float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,size", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe packets: %v", err)
	}
	bytes := map[int]int64{}
	last := -1
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 || fields[0] == "" || fields[0] == "N/A" {
			continue
		}
		pts, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			t.Fatalf("packet pts %q: %v", fields[0], err)
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("packet size %q: %v", fields[1], err)
		}
		second := int(math.Floor(pts))
		bytes[second] += size
		if second > last {
			last = second
		}
	}
	if last < 0 {
		t.Fatal("ffprobe found no video packets")
	}
	segs := make([]float64, last+1)
	for second, size := range bytes {
		segs[second] = float64(size) * 8 / 1000 // kbps
	}
	return segs
}

// wholeSeconds is the number of complete seconds of video to check: the clip's duration rounded
// down, and never more buckets than were measured.
func wholeSeconds(duration time.Duration, segs []float64) int {
	n := int(duration / time.Second)
	if n > len(segs) {
		n = len(segs)
	}
	return n
}

// TestRealCBRHitsItsTarget is the bitrate's own end-to-end proof: CBR holds every second near the
// ladder's rate, which a plain average does not. The second half of the clip is a fractal, far
// harder than the graphics before it, so an encoder that only averages spends its bits there.
// Measured on the static ffmpeg 7.0.2 build: CBR stays between 0.89 and 1.26 of the target every
// second, while the same plan without minrate/maxrate/bufsize runs from 0.80 to 1.68. The band is
// 0.7 to 1.4: generous for another x264 build, still failing a plain average.
func TestRealCBRHitsItsTarget(t *testing.T) {
	requireRealTools(t)
	if !listedEncoders(t)["libx264"] {
		if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
			t.Fatal("this ffmpeg build has no libx264 and SYP_REQUIRE_FFMPEG is set")
		}
		t.Skip("this ffmpeg build has no libx264")
	}
	dir := t.TempDir()

	source := filepath.Join(dir, "source.mp4")
	mk := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=5",
		"-f", "lavfi", "-i", "mandelbrot=size=640x360:rate=30",
		"-filter_complex",
		"[1:v]trim=duration=5,setpts=PTS-STARTPTS[b];[0:v][b]concat=n=2:v=1:a=0",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-crf", "18", source)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	info, err := media.Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}

	const target = 1.0 // Mb/s
	out := filepath.Join(dir, "out.mp4")
	plan := Plan{
		Video:       VideoPlan{Codec: CodecH264, Preset: "faster"},
		RateControl: &RateControl{Mode: "cbr", AvgMbps: target},
		Container:   ContainerMP4,
		Output:      out,
	}
	res, err := Build(plan, info, cpuRung(CodecH264))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	run, err := media.Run(context.Background(), append([]string{"ffmpeg"}, res.Argv...), nil)
	if err != nil {
		t.Fatalf("ffmpeg: %v (exit %d)\n%s", err, run.ExitCode, strings.Join(run.KeyLines, "\n"))
	}

	got, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	segs := cbrSegments(t, out)
	seconds := wholeSeconds(got.Format.Duration, segs)
	if seconds < 8 {
		t.Fatalf("only %d whole seconds measured, want a clip long enough to show a swing", seconds)
	}
	segs = segs[:seconds]

	rates := make([]string, len(segs))
	var bad []string
	for i, kbps := range segs {
		ratio := kbps / 1000 / target
		if ratio < 0.7 || ratio > 1.4 {
			bad = append(bad, fmt.Sprintf("second %d = %.2f Mb/s (%.2fx)", i, kbps/1000, ratio))
		}
		rates[i] = fmt.Sprintf("%.0f", kbps)
	}
	t.Logf("CBR second bitrates (kbps): %s against the %.1f Mb/s target", strings.Join(rates, " "), target)
	if len(bad) > 0 {
		t.Errorf("CBR left its band: %s; every second should stay within 0.7-1.4 of %.1f Mb/s",
			strings.Join(bad, ", "), target)
	}
}

// TestRealHEVCCBRIsStrict is why HEVC's CBR carries hrd and strict-cbr: a nearly static picture
// needs almost no bits, and libx265 without them encodes at a fraction of the rate (measured 13
// kbps against a 1 Mb/s target) instead of padding to it. The first second is left out because the
// encoder is still priming its buffer (measured 504 kbps there, 1004 after).
func TestRealHEVCCBRIsStrict(t *testing.T) {
	requireRealTools(t)
	if !listedEncoders(t)["libx265"] {
		if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
			t.Fatal("this ffmpeg build has no libx265 and SYP_REQUIRE_FFMPEG is set")
		}
		t.Skip("this ffmpeg build has no libx265")
	}
	dir := t.TempDir()

	source := filepath.Join(dir, "still.mp4")
	mk := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=gray:size=640x360:rate=30:duration=5",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-crf", "18", source)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	info, err := media.Probe(context.Background(), source)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}

	const target = 1.0 // Mb/s
	out := filepath.Join(dir, "out.mp4")
	plan := Plan{
		Video:       VideoPlan{Codec: CodecHEVC, Preset: "faster"},
		RateControl: &RateControl{Mode: "cbr", AvgMbps: target},
		Container:   ContainerMP4,
		Output:      out,
	}
	res, err := Build(plan, info, cpuRung(CodecHEVC))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	run, err := media.Run(context.Background(), append([]string{"ffmpeg"}, res.Argv...), nil)
	if err != nil {
		t.Fatalf("ffmpeg: %v (exit %d)\n%s", err, run.ExitCode, strings.Join(run.KeyLines, "\n"))
	}

	got, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	segs := cbrSegments(t, out)
	seconds := wholeSeconds(got.Format.Duration, segs)
	if seconds < 4 {
		t.Fatalf("only %d whole seconds measured", seconds)
	}

	rates := make([]string, len(segs[:seconds]))
	var bad []string
	for i := 1; i < seconds; i++ { // second 0 is the encoder priming its buffer
		ratio := segs[i] / 1000 / target
		if ratio < 0.7 || ratio > 1.3 {
			bad = append(bad, fmt.Sprintf("second %d = %.2f Mb/s (%.2fx)", i, segs[i]/1000, ratio))
		}
	}
	for i := range rates {
		rates[i] = fmt.Sprintf("%.0f", segs[i])
	}
	t.Logf("strict CBR second bitrates (kbps): %s against the %.1f Mb/s target", strings.Join(rates, " "), target)
	if len(bad) > 0 {
		t.Errorf("strict CBR did not hold the rate: %s; a static picture should be padded up to %.1f Mb/s",
			strings.Join(bad, ", "), target)
	}
}
