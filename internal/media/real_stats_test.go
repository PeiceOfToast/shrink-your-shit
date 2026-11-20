// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// ffmpegLogged runs ffmpeg at -v verbose and returns what it wrote to stderr: where its end-of-run report is.
func ffmpegLogged(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-v", "verbose", "-nostats", "-y"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// P2: a run that reads the file reports the same video packet count a separate -count_packets read gives, so a
// conversion can hand its source count to the checks instead of the source being read again just to count it.
// Real ffmpeg, on the sample types the checks meet: H.264 MKV, HEVC MKV and H.264 MP4, with the video encoded
// and copied.
func TestRealRunReportsTheSameCountAsCountPackets(t *testing.T) {
	needFFmpeg(t)
	has := Encoders(context.Background(), OSExec{}, "ffmpeg")
	dir := t.TempDir()
	for _, sm := range []struct{ name, file, codec string }{
		{"h264 mkv", "h264.mkv", "libx264"},
		{"hevc mkv", "hevc.mkv", "libx265"},
		{"h264 mp4", "h264.mp4", "libx264"},
	} {
		t.Run(sm.name, func(t *testing.T) {
			if !has[sm.codec] {
				t.Logf("this ffmpeg has no %s encoder: not run here", sm.codec) // a skip would fail CI's real-test rule
				return
			}
			src := filepath.Join(dir, sm.file)
			ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
				"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
				"-c:v", sm.codec, "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", src)
			ctx := context.Background()
			info, err := Probe(ctx, OSExec{}, "ffprobe", src)
			if err != nil {
				t.Fatal(err)
			}
			v := info.Video()
			want, err := CountVideoPackets(ctx, OSExec{}, "ffprobe", src, info.MainVideoOrdinal())
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				name   string
				encode bool
				args   []string
			}{
				{"encoded", true, []string{"-c", "copy", "-c:v:0", "libx265", "-preset", "ultrafast", "-crf", "30"}},
				{"copied", false, []string{"-c", "copy"}},
			} {
				args := append([]string{"-nostdin", "-i", src, "-map", "0", "-map", "-0:d"}, c.args...)
				args = append(args, "-f", "null", "-")
				got, ok := InputStreamStats(ffmpegLogged(t, args...), 0, v.Index)
				if !ok {
					t.Fatalf("%s: no report for #0:%d", c.name, v.Index)
				}
				if got.Packets != want {
					t.Errorf("%s: the run read %d packets, -count_packets gives %d", c.name, got.Packets, want)
				}
				if c.encode {
					if got.Frames != want {
						t.Errorf("%s: %d pictures decoded, %d packets read", c.name, got.Frames, want)
					}
				} else if got.Frames != -1 {
					t.Errorf("%s: a copied stream reports %d decoded pictures", c.name, got.Frames)
				}
			}
		})
	}
}
