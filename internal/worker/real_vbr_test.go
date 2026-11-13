// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-13 item A, done when: Bitrate level at 720 / Medium makes a 720p HEVC file whose average is
// the level's figure for HEVC (2.4 Mbps: the H.264 4 Mbps scaled by 60%), and VBR with a 3 Mbps max stays under
// that max, on a real, busy 1080p file (H.264 at 10 Mbps, so neither the level nor the max is above it).
func TestRealBitrateLevelAndVBRAt720(t *testing.T) {
	needTools(t)
	src := filepath.Join(t.TempDir(), "Busy.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25:duration=12", "-vf", "noise=alls=10:allf=t",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "10M", "-pix_fmt", "yuv420p", src)
	level := rules.DefaultSelectors(3)
	level.Resolution, level.MaxBitrate, level.RateControl, level.Audio, level.OnlyMyLanguages = "720", "medium", "level", "as_is", false
	vbr := rules.DefaultSelectors(3)
	vbr.Resolution, vbr.RateControl, vbr.VBRMaxKbps, vbr.Audio, vbr.OnlyMyLanguages = "720", "vbr", 3000, "as_is", false
	for name, sel := range map[string]rules.Selectors{"level": level, "vbr": vbr} {
		if err := sel.Validate(); err != nil {
			t.Fatal(err)
		}
		avg := realAverage720(t, src, sel.Template())
		t.Logf("%s: average %.0f kbps", name, avg)
		if name == "level" && (avg < 2400*0.85 || avg > 2400*1.15) {
			t.Errorf("level: average %.0f kbps, not close to 2400", avg)
		}
		if name == "vbr" && avg > 3000 {
			t.Errorf("vbr: average %.0f kbps is above the 3000 max", avg)
		}
	}
}

// realAverage720 encodes src with tmpl and returns the output's average video bitrate in kbps; the output is 720p.
func realAverage720(t *testing.T, src string, tmpl rules.Template) float64 {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.mkv")
	b, _ := os.ReadFile(src)
	os.WriteFile(in, b, 0o644)
	st, id := realJob(t, in, 12)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if h := probe(t, j.OutputPath).Video().Height; h != 720 {
		t.Errorf("height %d", h)
	}
	out, err := exec.CommandContext(context.Background(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=size",
		"-of", "csv=p=0", j.OutputPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, l := range strings.Fields(string(out)) {
		n, _ := strconv.ParseInt(strings.TrimSuffix(l, ","), 10, 64)
		bytes += n
	}
	return float64(bytes*8) / 12 / 1000
}

// Review a772397: every format VBR is offered for really encodes with a peak limit (HEVC, H.264, VP9);
// AV1 isn't offered VBR (SVT-AV1 takes a peak limit only with CRF), and its quality mode with a level
// encodes. Formats this ffmpeg lacks are skipped.
func TestRealVBREveryFormatOffered(t *testing.T) {
	needTools(t)
	encoders := media.Encoders(context.Background(), media.OSExec{}, "ffmpeg")
	rules.SetAvailableFormats(encoders)
	defer rules.SetAvailableFormats(nil)
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=3",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "8M", "-pix_fmt", "yuv420p", src)
	for _, c := range []struct{ format, rate, codec string }{
		{"hevc", "vbr", "hevc"}, {"h264", "vbr", "h264"}, {"vp9", "vbr", "vp9"}, {"av1", "quality_legacy", "av1"},
	} {
		t.Run(c.format+" "+c.rate, func(t *testing.T) {
			if !encoders[rules.VideoFormats[c.format].Encoder] {
				t.Skip("this ffmpeg has no " + rules.VideoFormats[c.format].Encoder)
			}
			in := filepath.Join(t.TempDir(), "clip.mkv")
			b, _ := os.ReadFile(src)
			os.WriteFile(in, b, 0o644)
			sel := rules.DefaultSelectors(3)
			sel.Format, sel.MaxBitrate, sel.Audio, sel.OnlyMyLanguages = c.format, "medium", "as_is", false
			if c.format == "vp9" {
				sel.FileType = "webm"
				sel.Audio = "stereo_opus"
			}
			sel.RateControl = c.rate // vbr: the max; level: the figure; quality_legacy: AV1 with a cap, as before
			if c.rate == "vbr" {
				sel.VBRMaxKbps = 6000
			}
			if err := sel.Validate(); err != nil {
				t.Fatal(err)
			}
			tmpl := sel.Template()
			st, id := realJob(t, in, 3)
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
			w.KeepDays, w.Journal = func() int { return 7 }, st // a WebM replaces an MKV: a new name, recorded
			if err := w.run(t); err != nil {
				t.Fatal(err)
			}
			j, _ := st.Get(id)
			if j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			if v := probe(t, j.OutputPath).Video(); v == nil || v.Codec != c.codec {
				t.Errorf("made %+v", v)
			}
		})
	}
}

// Review a772397 (pre-existing on dev): VP9 quality mode with each level, and none, really encodes
// (constrained quality: -crf with -b:v as the cap; libvpx refused -maxrate with -b:v 0).
func TestRealVP9QualityWithEachLevel(t *testing.T) {
	needTools(t)
	encoders := media.Encoders(context.Background(), media.OSExec{}, "ffmpeg")
	if !encoders["libvpx-vp9"] {
		t.Skip("this ffmpeg has no libvpx-vp9")
	}
	rules.SetAvailableFormats(encoders)
	defer rules.SetAvailableFormats(nil)
	src := filepath.Join(t.TempDir(), "clip.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "8M", "-pix_fmt", "yuv420p", src)
	for _, level := range []string{"low", "medium", "high", "none"} {
		t.Run(level, func(t *testing.T) {
			in := filepath.Join(t.TempDir(), "clip.mkv")
			b, _ := os.ReadFile(src)
			os.WriteFile(in, b, 0o644)
			sel := rules.DefaultSelectors(3)
			sel.Format, sel.FileType, sel.Audio, sel.OnlyMyLanguages, sel.MaxBitrate = "vp9", "webm", "stereo_opus", false, level
			if level != "none" {
				sel.RateControl = "level"
			}
			tmpl := sel.Template()
			st, id := realJob(t, in, 2)
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
			w.KeepDays, w.Journal = func() int { return 7 }, st
			if err := w.run(t); err != nil {
				t.Fatal(err)
			}
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			} else if v := probe(t, j.OutputPath).Video(); v == nil || v.Codec != "vp9" {
				t.Errorf("made %+v", v)
			}
		})
	}
}
