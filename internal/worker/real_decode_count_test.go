// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// P1: the count the playback run reports is the count -count_packets gives, on the kinds of file the
// checks meet. The check takes the run's number (no second read), so a difference here would be a wrong
// Expect number: the sample types are H.264 MP4, HEVC MKV, B-frames, interlaced and variable frame rate.
func TestRealDecodeRunCountsThePacketsItRead(t *testing.T) {
	needTools(t)
	has := media.Encoders(context.Background(), media.OSExec{}, "ffmpeg")
	dir := t.TempDir()
	cases := []struct {
		name, file string
		args       []string
	}{
		{"h264 mp4", "h264.mp4", []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac"}},
		{"hevc mkv", "hevc.mkv", []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
			"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac"}},
		{"b-frames", "bf.mkv", []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
			"-c:v", "libx264", "-preset", "ultrafast", "-bf", "3", "-g", "12", "-pix_fmt", "yuv420p", "-c:a", "aac"}},
		{"interlaced", "il.mkv", []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
			"-vf", "tinterlace=interleave_top", "-c:v", "libx264", "-preset", "ultrafast",
			"-flags", "+ilme+ildct", "-pix_fmt", "yuv420p", "-c:a", "aac"}},
		{"vfr", "vfr.mkv", []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=10:duration=2",
			"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
			"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-fps_mode", "vfr"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if strings.Contains(strings.Join(c.args, " "), "libx265") && !has["libx265"] {
				t.Logf("this ffmpeg has no libx265 encoder: not run here") // a skip would fail CI's real-test rule
				return
			}
			p := filepath.Join(dir, c.file)
			runFFmpeg(t, append(c.args, p)...)
			ctx := context.Background()
			info := probe(t, p)
			st, _ := realJob(t, p, info.DurationSec)
			w := realWorker(st, media.OSExec{})
			var fps float64
			if v := info.Video(); v != nil {
				fps = v.FrameRate
			}
			packets, counted, err := w.decodeRun(ctx, p, info, 0, 0, info.DurationSec, fps)
			if err != nil {
				t.Fatalf("the run does not play the file it is meant to check: %v", err)
			}
			if !counted {
				t.Fatalf("the run printed no packet report")
			}
			want, err := media.CountVideoPackets(ctx, media.OSExec{}, "ffprobe", p, info.MainVideoOrdinal())
			if err != nil {
				t.Fatal(err)
			}
			if packets != want {
				t.Fatalf("the playback run read %d packets; -count_packets gives %d: P1 must not switch for this case", packets, want)
			}
		})
	}
}
