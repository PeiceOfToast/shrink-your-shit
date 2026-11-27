// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// clipNames are the generated clips the real tests probe.
var clipNames = []string{
	"h264-aac.mp4",
	"hevc10.mkv",
	"surround.mp4",
	"multilingual.mkv",
	"hdr10.mkv",
	"cover.mp4",
}

// generateClips makes the short clips with ffmpeg's own sources in a temporary folder and returns
// their paths by name. Nothing is committed: every run makes them again (backend-plan §16, "Real
// media").
func generateClips(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()

	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	subtitle := write("english.srt", "1\n00:00:00,000 --> 00:00:01,000\nHello from the test clip\n")
	chapters := write("chapters.txt", ";FFMETADATA1\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=500\ntitle=Intro\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=500\nEND=1000\ntitle=Main\n")

	ffmpeg := func(args ...string) {
		t.Helper()
		full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
		cmd := exec.Command("ffmpeg", full...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}

	// H.264 + AAC stereo in MP4.
	ffmpeg("-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k", "-shortest", "h264-aac.mp4")

	// HEVC 10-bit in MKV.
	ffmpeg("-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=1.5",
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error", "hevc10.mkv")

	// 5.1 audio.
	ffmpeg("-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-af", "aformat=channel_layouts=5.1", "-ac", "6", "-b:a", "192k",
		"-shortest", "surround.mp4")

	// Two audio languages, an SRT subtitle and two chapters.
	ffmpeg("-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=2",
		"-i", subtitle, "-i", chapters,
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s", "-map_metadata", "4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-c:s", "srt", "-shortest",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=English",
		"-metadata:s:a:1", "language=ger", "-metadata:s:a:1", "title=Deutsch",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "title=English",
		"multilingual.mkv")

	// HDR10 tags (metadata only: the pixel values are ordinary 10-bit).
	ffmpeg("-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=1.5",
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
		"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400",
		"hdr10.mkv")

	// Cover art: a PNG attached to an MP4, which ffprobe reports as a second video stream.
	ffmpeg("-f", "lavfi", "-i", "color=red:size=64x64:duration=0.04", "-frames:v", "1", "cover.png")
	ffmpeg("-i", "h264-aac.mp4", "-i", "cover.png", "-map", "0", "-map", "1",
		"-c", "copy", "-disposition:v:1", "attached_pic", "cover.mp4")

	clips := make(map[string]string, len(clipNames))
	for _, name := range clipNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("clip %s was not made: %v", name, err)
		}
		clips[name] = path
	}
	return clips
}
