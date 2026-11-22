// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

var lufsSummary = regexp.MustCompile(`I:\s+(-?[0-9.]+) LUFS`)

// lufsOf is the integrated loudness of the ord-th audio stream of path, by ebur128 (the summary's last line).
func lufsOf(t *testing.T, path string, ord int) float64 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-nostats", "-i", path,
		"-map", fmt.Sprintf("0:a:%d", ord), "-vn", "-sn", "-af", "ebur128=framelog=quiet", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("ebur128 on %s: %v\n%s", path, err, out)
	}
	m := lufsSummary.FindAllStringSubmatch(string(out), -1)
	if len(m) == 0 {
		t.Fatalf("no integrated loudness in ebur128 output:\n%s", out)
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// loudSource makes a file of video and two stereo 48 kHz audio tracks: a FLAC sine at about -30 LUFS (to be
// evened out) and an AC-3 sine at 96 kbps (copied, as the Plan says a small AC-3 is).
func loudSource(t *testing.T, path string, dur int) {
	t.Helper()
	runFFmpeg(t, "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=duration=%d:size=320x180:rate=25", dur),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=duration=%d:frequency=440:sample_rate=48000", dur),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=duration=%d:frequency=660:sample_rate=48000", dur),
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a:0", "flac", "-ac:a:0", "2", "-filter:a:0", "volume=-11dB",
		"-c:a:1", "ac3", "-b:a:1", "96k", "-ac:a:1", "2", path)
}

// The even-out on a real file (F5): with the setting on, the converted track ends within 1 LU of -24 LUFS, its
// sample rate stays the source's, and the copied track keeps its loudness; with it off, nothing moves.
func TestRealEvenLoudness(t *testing.T) {
	needTools(t)
	src := filepath.Join(t.TempDir(), "src.mkv")
	loudSource(t, src, 8)
	srcLoud := lufsOf(t, src, 0)
	t.Logf("source converted track measures %.2f LUFS", srcLoud)
	if srcLoud < -34 || srcLoud > -26 {
		t.Fatalf("the test source is at %.1f LUFS, not near -30: the test would prove nothing", srcLoud)
	}
	srcCopy := lufsOf(t, src, 1)
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("setting on=%v", on), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "m.mkv")
			b, _ := os.ReadFile(src)
			os.WriteFile(p, b, 0o644)
			s := rules.DefaultSelectors(3)
			s.Audio, s.OnlyMyLanguages, s.Subtitles = "stereo_aac", false, "keep_all"
			s.EvenLoudness = on
			tmpl := s.Template()
			tmpl.Languages = ""
			st, id := realJob(t, p, 8)
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
			if err := w.run(t); err != nil {
				t.Fatal(err)
			}
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			info := probe(t, p)
			var audio []media.Stream
			for _, s := range info.Streams {
				if s.Type == "audio" {
					audio = append(audio, s)
				}
			}
			if len(audio) != 2 {
				t.Fatalf("%d audio tracks, want 2", len(audio))
			}
			for _, s := range audio {
				if s.SampleRate != 48000 {
					t.Errorf("audio track %d is %d Hz, want the source's 48000", s.Index, s.SampleRate)
				}
			}
			converted := lufsOf(t, p, 0)
			copied := lufsOf(t, p, 1)
			t.Logf("setting %v: converted %.2f LUFS, copied %.2f LUFS (source copied %.2f)", on, converted, copied, srcCopy)
			if on && math.Abs(converted+24) > 1 {
				t.Errorf("setting on: converted track is %.2f LUFS, want -24 within 1 LU", converted)
			}
			if !on && math.Abs(converted+24) <= 1 {
				t.Errorf("setting off: converted track was normalised to %.2f LUFS", converted)
			}
			if math.Abs(copied-srcCopy) > 0.1 {
				t.Errorf("copied track moved from %.2f to %.2f LUFS", srcCopy, copied)
			}
		})
	}
}

// How long a measuring run takes on a long track: the basis for loudnessTotalLimit (F5). Logged, not asserted.
func TestRealLoudnessMeasureTime(t *testing.T) {
	needTools(t)
	if testing.Short() {
		t.Skip("a ten-minute track: skipped in -short")
	}
	long := filepath.Join(t.TempDir(), "long.mkv")
	loudSource(t, long, 600)
	w := realWorker(nil, media.OSExec{})
	src := probe(t, long)
	op := rules.TrackOp{Index: 1, Type: "audio", Encoder: "aac", Channels: 2}
	start := time.Now()
	m, err := w.measureTrack(context.Background(), long, src, op)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("measuring a 600 s track took %s (%.1f LUFS)", took, m.I)
}
