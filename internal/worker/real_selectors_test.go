// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
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
		convert(t, p, sel(func(s *rules.Selectors) { s.AfterConverting, s.FileType = "beside", "keep" })) // S6's default is MKV
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
	// "subtitles MP4 cannot hold" was SRT; SRT now becomes mov_text (TestRealMP4KeepsTextSubtitles). The
	// skip that's left is for picture subtitles (PGS, DVB), which ffmpeg can't make from text, so the rules
	// test TestMP4TextSubtitles covers it with a probed PGS track.
	t.Run("HDR10 is converted keeping its HDR (S3; TestRealHDR10 runs it)", func(t *testing.T) {
		// x264's own VUI flags: ffmpeg's -color_trc does not reach an mkv stream from libx264
		p := gen("hdr.mkv", "1280x720", 5000, "-pix_fmt", "yuv420p10le", "-x264-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc")
		info := probe(t, p)
		if plan := rules.SliderTemplate(3).Compile(info); plan.Skip != "" || plan.Video.HDR == nil || plan.Video.HDR.Transfer != "smpte2084" {
			t.Errorf("slider: %q %+v", plan.Skip, plan.Video.HDR)
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
	capAvg, capPeak := encode("high") // S6: High is 8 Mbps at 1080p HEVC (research's ladder)
	t.Logf("uncapped: avg %.1f Mbps, peak 1 s %.1f Mbps; capped at High (8): avg %.1f, peak %.1f", freeAvg, freePeak, capAvg, capPeak)
	if freeAvg < 15 {
		t.Skipf("the test file is not demanding enough here (uncapped average %.1f Mbps)", freeAvg)
	}
	// VBV: maxrate 8 Mbps with a 16 Mb buffer, which starts full: over the 4 s the average can be up to
	// 8 + 16/4 Mbps, and one second can hold at most maxrate plus the buffer
	if capAvg > 8+16/4.0 || capPeak > 24 {
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

// Basic's defaults on real files (S4): (1) stereo AAC in my language: a 5.1 English track becomes stereo
// AAC, a French one is dropped; (2) an HEVC MP4 is repackaged as Movie.mkv with its video copied, not
// encoded; (3) an MP4's mov_text subtitle becomes SRT text inside the MKV, the text kept.
func TestRealBasicDefaults(t *testing.T) {
	needTools(t)
	basicRun := func(t *testing.T, src string, audio string) (*jobs.Store, int64) {
		tmpl := rules.BasicTemplate(3, audio)
		tmpl.Languages = "eng"
		st, id := realJob(t, src, 5)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		w.KeepDays, w.Journal = func() int { return 7 }, st // the .mp4 to .mkv rename is journaled (25a-2)
		w.run(t)
		return st, id
	}
	t.Run("stereo AAC in my language", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "Movie.mkv")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25",
			"-f", "lavfi", "-i", "sine=duration=5:frequency=440", "-f", "lavfi", "-i", "sine=duration=5:frequency=660",
			"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-b:v", "5000k",
			"-c:a:0", "eac3", "-ac:a:0", "6", "-c:a:1", "aac", "-ac:a:1", "2",
			"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fre", src)
		st, id := basicRun(t, src, "")
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
		a := probe(t, src).Audio()
		if len(a) != 1 || a[0].Codec != "aac" || a[0].Channels != 2 || a[0].Language != "eng" {
			t.Errorf("audio %+v", a)
		}
	})
	t.Run("HEVC MP4 repackaged as MKV", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "Movie.mp4")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=5",
			"-c:v", "libx265", "-preset", "ultrafast", "-tag:v", "hvc1", "-c:a", "aac", "-ac", "2", "-metadata:s:a:0", "language=eng", src)
		before := probe(t, src)
		st, id := basicRun(t, src, "")
		mkv := filepath.Join(dir, "Movie.mkv")
		if j, _ := st.Get(id); j.Status != jobs.Done || j.OutputPath != mkv {
			t.Fatalf("%+v", j)
		}
		after := probe(t, mkv)
		if after.Container != "matroska,webm" || after.Video().Codec != "hevc" || after.Video().Width != before.Video().Width {
			t.Errorf("Movie.mkv: %s %+v", after.Container, after.Video())
		}
		if snap := plan(t, st, id); !snap.Video.Copy {
			t.Error("the video was encoded, not copied")
		}
	})
	t.Run("mov_text becomes SRT text in the MKV", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nhello there\n"), 0o644)
		src := filepath.Join(dir, "Movie.mp4")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=5",
			"-i", filepath.Join(dir, "a.srt"), "-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-b:v", "5000k",
			"-c:a", "aac", "-ac", "2", "-c:s", "mov_text", "-metadata:s:a:0", "language=eng", "-metadata:s:s:0", "language=eng", src)
		st, id := basicRun(t, src, "")
		mkv := filepath.Join(dir, "Movie.mkv")
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
		subs := probe(t, mkv).Subtitles()
		if len(subs) != 1 || subs[0].Codec != "subrip" || subs[0].Language != "eng" {
			t.Fatalf("subtitles %+v", subs)
		}
		out, err := exec.Command("ffmpeg", "-v", "error", "-i", mkv, "-map", "0:s:0", "-f", "srt", "-").Output()
		if err != nil || !strings.Contains(string(out), "hello there") {
			t.Errorf("the text: %q %v", out, err)
		}
	})
}

// plan is the Plan job id ran with (its snapshot).
func plan(t *testing.T, st *jobs.Store, id int64) rules.Plan {
	t.Helper()
	j, _ := st.Get(id)
	var snap struct{ Plan rules.Plan }
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatalf("plan %q: %v", j.Plan, err)
	}
	return snap.Plan
}

// "Save to another folder" (S6): the new file goes to the folder for new files, in the same sub-folder
// as in its library; the original stays exactly as it was; a name already taken there is never replaced.
func TestRealSaveToAnotherFolder(t *testing.T) {
	needTools(t)
	lib, out := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(lib, "Show", "S01"), 0o755)
	src := filepath.Join(lib, "Show", "S01", "e1.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-c:v", "libx264", "-b:v", "2000k", src)
	before, _ := os.ReadFile(src)
	s := rules.DefaultSelectors(3)
	s.AfterConverting, s.OutputDir = "folder", out
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	run := func() jobs.Job {
		st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		st.SetPaused(false) // a new store starts with its worker stopped (S2)
		id, _ := st.Enqueue(jobs.Job{Library: lib, Path: src, DurationSec: 3})
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		w.run(t)
		j, _ := st.Get(id)
		return j
	}
	want := filepath.Join(out, "Show", "S01", "e1.mkv") // MKV: design E's default type
	if j := run(); j.Status != jobs.Done || j.OutputPath != want {
		t.Fatalf("%s %q -> %s", j.Status, j.Error, j.OutputPath)
	}
	if v := probe(t, want).Video(); v.Codec != "hevc" {
		t.Errorf("new file %+v", v)
	}
	if now, _ := os.ReadFile(src); string(now) != string(before) {
		t.Error("the original changed")
	}
	if tempLeft(want) || tempLeft(src) {
		t.Error("a temp file was left")
	}
	// again: the name is taken in the folder for new files, so nothing is made or replaced
	made, _ := os.ReadFile(want)
	if j := run(); j.Status != jobs.Failed || !strings.Contains(j.Error, "in the folder for new files") {
		t.Errorf("name taken: %s %q", j.Status, j.Error)
	}
	if again, _ := os.ReadFile(want); string(again) != string(made) {
		t.Error("the file in the folder for new files was replaced")
	}
}

// S6: one real file to each video format this ffmpeg can make (skipped where its encoder is missing):
// the job ends done, in that codec, past every check (the picture comparison at Balanced included).
func TestRealEachVideoFormat(t *testing.T) {
	needTools(t)
	rules.SetAvailableFormats(media.Encoders(context.Background(), media.OSExec{}, "ffmpeg"))
	defer rules.SetAvailableFormats(nil)
	for _, f := range []struct{ format, codec, fileType string }{{"h264", "h264", "mkv"}, {"av1", "av1", "mkv"}, {"vp9", "vp9", "mkv"}} {
		t.Run(f.format, func(t *testing.T) {
			if !slices.Contains(rules.Available(), f.format) {
				t.Logf("this ffmpeg has no encoder for %s: not run here", f.format) // a skip would fail CI's real-test rule
				return
			}
			dir := t.TempDir()
			src := filepath.Join(dir, "m.mkv")
			runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-c:v", "mpeg2video", "-b:v", "6000k", src)
			s := rules.DefaultSelectors(3)
			s.Format, s.Speed, s.FileType = f.format, "faster", f.fileType
			tmpl := s.Template()
			st, id := realJob(t, src, 3)
			st.SetPaused(false)
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
			w.run(t)
			j, _ := st.Get(id)
			if j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			if v := probe(t, j.OutputPath).Video(); v.Codec != f.codec {
				t.Errorf("new file %+v", v)
			}
		})
	}
}

// QA 894cb18 (P2): VP9 + WebM + Stereo Opus on a file with a 128 kbps stereo AAC track (the size Stereo
// Opus would otherwise keep as it is) ends done, its audio made Opus: WebM can't hold AAC.
func TestRealWebMWithSmallAAC(t *testing.T) {
	needTools(t)
	rules.SetAvailableFormats(media.Encoders(context.Background(), media.OSExec{}, "ffmpeg"))
	defer rules.SetAvailableFormats(nil)
	if !slices.Contains(rules.Available(), "vp9") {
		t.Log("this ffmpeg has no VP9 encoder: not run here") // a skip would fail CI's real-test rule
		return
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "mpeg2video", "-b:v", "6000k", "-c:a", "aac", "-b:a", "128k", "-ac", "2", src)
	s := rules.DefaultSelectors(3)
	s.Format, s.Speed, s.FileType, s.Audio, s.OnlyMyLanguages = "vp9", "faster", "webm", "stereo_opus", false
	s.AfterConverting = "beside" // the original stays: no record of kept originals needed for the new type
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	st.SetPaused(false)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := probe(t, j.OutputPath)
	if filepath.Ext(j.OutputPath) != ".webm" || out.Video().Codec != "vp9" || len(out.Audio()) != 1 || out.Audio()[0].Codec != "opus" {
		t.Errorf("new file %s: %+v", j.OutputPath, out.Streams)
	}
}

// Backlog after #93: a .webm is scanned, and "Keep the file type" makes it MKV when the new file has
// audio WebM can't hold (here Stereo AAC from a 5.1 track, always converted), with the log's note.
// The source is H.264 written as Matroska under a .webm name, so any ffmpeg with VP9 runs it: the rule
// goes by the file's extension (ffprobe names WebM and MKV alike).
func TestRealKeepWebMBecomesMKV(t *testing.T) {
	needTools(t)
	rules.SetAvailableFormats(media.Encoders(context.Background(), media.OSExec{}, "ffmpeg"))
	defer rules.SetAvailableFormats(nil)
	if !slices.Contains(rules.Available(), "vp9") {
		t.Log("this ffmpeg has no VP9 encoder: not run here") // a skip would fail CI's real-test rule
		return
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.webm")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libx264", "-b:v", "4000k", "-c:a", "ac3", "-ac", "6", "-f", "matroska", src)
	s := rules.DefaultSelectors(3)
	s.Format, s.Speed, s.FileType, s.Audio, s.OnlyMyLanguages = "vp9", "faster", "keep", "stereo_aac", false
	s.AfterConverting = "beside" // the original stays: no record of kept originals needed for the new type
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	st.SetPaused(false)
	w := realWorker(st, media.OSExec{})
	w.Logs = &runlog.Logs{Dir: t.TempDir()}
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.run(t)
	j, _ := st.Get(id)
	text, _, _ := w.Logs.Read(id, 0)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q\n%s", j.Status, j.Error, text)
	}
	out := probe(t, j.OutputPath)
	if filepath.Ext(j.OutputPath) != ".mkv" || out.Video().Codec != "vp9" || len(out.Audio()) != 1 || out.Audio()[0].Codec != "aac" {
		t.Errorf("new file %s: %+v", j.OutputPath, out.Streams)
	}
	if !strings.Contains(text, "File type changed to MKV: WebM files can't hold AAC.") {
		t.Errorf("log:\n%s", text)
	}
}

// MP4 output (02:41Z): an MKV with SRT subtitles becomes an MP4 whose subtitles are MP4's own
// (mov_text), instead of the file being left alone. The SRT sample is made here; no media is committed.
func TestRealMP4KeepsTextSubtitles(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nHello there\n"), 0o644)
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-i", srt,
		"-map", "0", "-map", "1", "-c:v", "mpeg2video", "-b:v", "6000k", "-c:s", "srt", "-metadata:s:s:0", "language=eng", src)
	s := rules.DefaultSelectors(3)
	s.Speed, s.FileType, s.AfterConverting = "faster", "mp4", "beside" // beside: the original stays, no record of kept originals needed
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	st.SetPaused(false)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := probe(t, j.OutputPath)
	subs := out.Subtitles()
	if filepath.Ext(j.OutputPath) != ".mp4" || len(subs) != 1 || subs[0].Codec != "mov_text" || subs[0].Language != "eng" {
		t.Errorf("new file %s: %+v", j.OutputPath, out.Streams)
	}
}

// dropSubtitleExec runs real ffmpeg but makes the encode lose the second subtitle track.
type dropSubtitleExec struct{ media.OSExec }

func (d dropSubtitleExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for i, a := range args {
		if a == "-map" && i+1 < len(args) && args[i+1] == "-0:d" {
			args = append(append(append([]string{}, args[:i+2]...), "-map", "-0:s:1"), args[i+2:]...)
			break
		}
	}
	return d.OSExec.Stream(ctx, onLine, name, args...)
}

// #117: MP4's mov_text skips only the default-flag comparison; a subtitle track lost on the way is still
// refused (count, codec and language are checked) and the original stays.
func TestRealMP4LostSubtitleIsRefused(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	en, fr := filepath.Join(dir, "en.srt"), filepath.Join(dir, "fr.srt")
	os.WriteFile(en, []byte("1\n00:00:00,500 --> 00:00:02,000\nHello\n"), 0o644)
	os.WriteFile(fr, []byte("1\n00:00:00,500 --> 00:00:02,000\nBonjour\n"), 0o644)
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-i", en, "-i", fr,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "mpeg2video", "-b:v", "6000k", "-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", src)
	orig, _ := os.ReadFile(src)
	s := rules.DefaultSelectors(3)
	s.Speed, s.FileType, s.AfterConverting, s.Subtitles = "faster", "mp4", "beside", "keep_all"
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	st.SetPaused(false)
	w := realWorker(st, dropSubtitleExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "subtitle") {
		t.Fatalf("a lost subtitle must be refused: %s %q", j.Status, j.Error)
	}
	if now, _ := os.ReadFile(src); string(now) != string(orig) {
		t.Error("the original changed")
	}
	if tempLeft(src) {
		t.Error("temp file left")
	}
}
