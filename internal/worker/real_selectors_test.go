// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Standard selectors on real files (slice 25a): each choice changes the real ffmpeg output as
// intended, and the result still passes every check (the picture comparison included).
func TestRealSelectors(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	gen := func(name, size string, kbps int, extra ...string) string {
		p := filepath.Join(dir, name)
		// 5 s, so the length check's 2% (100 ms) is well above an audio frame's padding
		args := []string{"-f", "lavfi", "-i", "testsrc2=duration=5:size=" + size + ":rate=25",
			"-f", "lavfi", "-i", "sine=duration=5", "-c:v", "libx264", "-b:v", fmt.Sprint(kbps) + "k", "-c:a", "aac"}
		runFFmpeg(t, append(append(args, extra...), p)...)
		return p
	}
	sel := func(change func(*rules.Selectors)) rules.Template {
		s := rules.DefaultSelectors(3)
		change(&s)
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		return s.Template()
	}
	convert := func(t *testing.T, path string, tmpl rules.Template) {
		t.Helper()
		if p := tmpl.Compile(probe(t, path)); p.Skip != "" {
			t.Fatalf("the scanner would skip it: %s", p.Skip)
		}
		st, id := realJob(t, path, 5)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		if err := w.run(t); err != nil {
			t.Fatal(err)
		}
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
	}

	t.Run("1080p capped to 720p, picture check passes", func(t *testing.T) {
		p := gen("big.mkv", "1920x1080", 8000)
		convert(t, p, sel(func(s *rules.Selectors) { s.Resolution = "720" }))
		if v := probe(t, p).Video(); v == nil || v.Codec != "hevc" || v.Width != 1280 || v.Height != 720 {
			t.Errorf("%+v", v)
		}
	})
	t.Run("never made bigger", func(t *testing.T) {
		p := gen("small480.mkv", "854x480", 3000)
		tmpl := sel(func(s *rules.Selectors) { s.Resolution = "720" })
		if plan := tmpl.Compile(probe(t, p)); plan.Video.Height != 0 {
			t.Errorf("a 480p file would be scaled to %d", plan.Video.Height)
		}
		convert(t, p, tmpl)
		if v := probe(t, p).Video(); v == nil || v.Height != 480 || v.Width != 854 {
			t.Errorf("%+v", v)
		}
	})
	t.Run("mp4 to mkv, kept beside", func(t *testing.T) {
		p := gen("clip.mp4", "1280x720", 5000)
		before, _ := os.ReadFile(p)
		convert(t, p, sel(func(s *rules.Selectors) { s.FileType = "mkv"; s.AfterConverting = "beside" }))
		out := filepath.Join(dir, "clip.hevc.mkv")
		info := probe(t, out)
		if !strings.Contains(info.Container, "matroska") || info.Video().Codec != "hevc" || len(info.Audio()) != 1 {
			t.Errorf("new file %s: %+v", out, info)
		}
		if after, _ := os.ReadFile(p); string(after) != string(before) {
			t.Error("the original must stay as it was")
		}
		if tempLeft(p) {
			t.Error("temp left")
		}
	})
	t.Run("same type, kept beside", func(t *testing.T) {
		p := gen("keep.mp4", "1280x720", 5000)
		convert(t, p, sel(func(s *rules.Selectors) { s.AfterConverting = "beside" }))
		if info := probe(t, filepath.Join(dir, "keep.hevc.mp4")); !strings.Contains(info.Container, "mp4") {
			t.Errorf("%+v", info)
		}
	})
	t.Run("slower is x265's slow preset", func(t *testing.T) {
		p := gen("slow.mkv", "1280x720", 5000)
		convert(t, p, sel(func(s *rules.Selectors) { s.Speed = "slower" }))
		b, _ := os.ReadFile(p)
		// x265 writes its settings into the file: medium uses rd=3, slow rd=4
		if rd := regexp.MustCompile(` rd=[0-9]+`).FindString(string(b)); rd != " rd=4" {
			t.Errorf("got %q", rd)
		}
	})
	t.Run("subtitles MP4 cannot hold: skipped with a reason", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "s.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644)
		p := filepath.Join(dir, "subs.mkv")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=2:size=1280x720:rate=25", "-i", filepath.Join(dir, "s.srt"),
			"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "5000k", "-c:s", "srt", p)
		plan := sel(func(s *rules.Selectors) { s.FileType = "mp4"; s.AfterConverting = "beside" }).Compile(probe(t, p))
		if plan.SkipCode != rules.SkipCannotKeepTrack || plan.Skip != "its subrip subtitles cannot be kept in MP4" {
			t.Errorf("%q %q", plan.SkipCode, plan.Skip)
		}
	})
	t.Run("HDR is skipped unless Skip HDR is off", func(t *testing.T) {
		// x264's own VUI flags: ffmpeg's -color_trc does not reach an mkv stream from libx264
		p := gen("hdr.mkv", "1280x720", 5000, "-pix_fmt", "yuv420p10le", "-x264-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc")
		info := probe(t, p)
		if plan := rules.SliderTemplate(3).Compile(info); plan.SkipCode != rules.SkipHDR {
			t.Errorf("slider: %q", plan.Skip)
		}
		if plan := sel(func(s *rules.Selectors) { s.SkipHDR = false }).Compile(info); plan.Skip != "" {
			t.Errorf("skipHdr off: %q", plan.Skip)
		}
	})
}

// The 10 Mbps cap on real output (maxBitrate, addendum 9): the same file with and without the cap; the
// capped one stays within the VBV limit in every second, the uncapped one goes well above it. Run on the
// Plan's own ffmpeg arguments, so the picture check (which a low cap on noise would fail) is not in the way.
func TestRealMaxBitrateCap(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "noisy.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=4:size=1920x1080:rate=25", "-vf", "noise=alls=30:allf=t",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "12", src)
	info := probe(t, src)
	encode := func(maxBitrate string) (avg, peak float64) {
		s := rules.DefaultSelectors(5)
		s.MaxBitrate, s.Speed = maxBitrate, "faster"
		plan := s.Template().Compile(info)
		if plan.Skip != "" {
			t.Fatal(plan.Skip)
		}
		out := filepath.Join(dir, "out-"+maxBitrate+".mkv")
		args := plan.FFmpegArgs(src, out)
		if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		return bitrates(t, out)
	}
	freeAvg, freePeak := encode("none")
	capAvg, capPeak := encode("10")
	t.Logf("uncapped: avg %.1f Mbps, peak 1 s %.1f Mbps; capped at 10: avg %.1f, peak %.1f", freeAvg, freePeak, capAvg, capPeak)
	if freeAvg < 15 {
		t.Skipf("the test file is not demanding enough here (uncapped average %.1f Mbps)", freeAvg)
	}
	// VBV: maxrate 10 Mbps with a 20 Mb buffer, which starts full: over the 4 s the average can be up to
	// 10 + 20/4 Mbps, and one second can hold at most maxrate plus the buffer
	if capAvg > 10+20/4.0 || capPeak > 30 {
		t.Errorf("capped at 10 Mbps: average %.1f, peak second %.1f", capAvg, capPeak)
	}
	if capPeak >= freePeak {
		t.Errorf("the cap changed nothing: peak %.1f vs %.1f uncapped", capPeak, freePeak)
	}
}

// bitrates are the video's average and its largest one-second bitrate, in Mbps, from its packets.
func bitrates(t *testing.T, path string) (avg, peak float64) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,size", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	perSecond := map[int]float64{}
	total, last := 0.0, 0.0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 2 {
			continue
		}
		ts, err1 := strconv.ParseFloat(f[0], 64)
		size, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		perSecond[int(ts)] += size * 8 / 1e6
		total += size * 8 / 1e6
		last = max(last, ts)
	}
	for _, v := range perSecond {
		peak = max(peak, v)
	}
	return total / max(last, 1), peak
}

// Replace mode with "Make it an MKV file" on a real MP4 (25a-2): Movie.mp4 becomes Movie.mkv (a real
// Matroska file, audio kept), the original is kept, and Get original back brings Movie.mp4 back and
// removes Movie.mkv.
func TestRealReplaceWithNewFileType(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=5:frequency=440",
		"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "5000k", "-c:a", "aac", "-b:a", "96k", "-metadata:s:a:0", "language=eng", src)
	before, _ := os.ReadFile(src)
	sel := rules.DefaultSelectors(3)
	sel.FileType = "mkv"
	tmpl := sel.Template()
	st, id := realJob(t, src, 5)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	w.run(t)
	mkv := filepath.Join(dir, "Movie.mkv")
	if j, _ := st.Get(id); j.Status != jobs.Done || j.OutputPath != mkv {
		t.Fatalf("%+v", j)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("Movie.mp4 is still there")
	}
	info := probe(t, mkv)
	if info.Container != "matroska,webm" || info.Video().Codec != "hevc" || len(info.Audio()) != 1 {
		t.Errorf("Movie.mkv: %s %s, %d audio", info.Container, info.Video().Codec, len(info.Audio()))
	}
	o, err := st.OriginalOfJob(id)
	if err != nil || o.NewPath != mkv {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := Restore(st, o.ID, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(src); string(b) != string(before) {
		t.Error("Movie.mp4 did not come back as it was")
	}
	if _, err := os.Stat(mkv); err == nil {
		t.Error("Movie.mkv is still there after getting the original back")
	}
}
