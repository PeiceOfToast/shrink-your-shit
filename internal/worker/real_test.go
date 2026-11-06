// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// These run the worker for real: real ffmpeg, real ffprobe, real files. They are skipped when ffmpeg
// is not installed, so the test suite never depends on it.

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// realJob queues a job for path and returns the store and job id.
func realJob(t *testing.T, path string, dur float64) (*jobs.Store, int64) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	id, err := st.Enqueue(jobs.Job{Library: filepath.Dir(path), Path: path, DurationSec: dur})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

// realWorker uses the fast preset and the default safety settings (SSIM threshold 0.95).
func realWorker(st *jobs.Store, x media.Exec) *Worker {
	r := rules.Default()
	r.Preset, r.CRF = "ultrafast", 28
	return &Worker{ID: "real", Queue: st, Exec: x, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Rule: r, Mode: Replace}
}

func probe(t *testing.T, path string) media.Info {
	t.Helper()
	info, err := media.Probe(context.Background(), media.OSExec{}, "ffprobe", path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// richFile makes a file with everything the safety checks look at: two audio tracks with languages,
// two subtitle tracks (one forced), a font attachment, chapters; video at the given pixel format.
func richFile(t *testing.T, dir, pixFmt string) string {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "s.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "s.ass"), []byte("[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Default,Arial,20\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,hi\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ch.txt"), []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=4000\ntitle=A\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=4000\nEND=8000\ntitle=B\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "f.ttf"), []byte("font bytes"), 0o644)
	out := filepath.Join(dir, "movie.mkv")
	runFFmpeg(t,
		"-f", "lavfi", "-i", "testsrc2=duration=8:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=duration=8", "-f", "lavfi", "-i", "sine=frequency=660:duration=8",
		"-i", filepath.Join(dir, "s.srt"), "-i", filepath.Join(dir, "s.ass"), "-i", filepath.Join(dir, "ch.txt"),
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4", "-map_metadata", "5",
		"-c:v", "libx264", "-pix_fmt", pixFmt, "-b:v", "3M", "-c:a", "aac", "-c:s:0", "srt", "-c:s:1", "ass",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", "-disposition:s:1", "forced",
		"-attach", filepath.Join(dir, "f.ttf"), "-metadata:s:t", "mimetype=application/x-truetype-font", out)
	return out
}

func TestRealFileWithEverythingIsConvertedAndVerified(t *testing.T) {
	needTools(t)
	for _, pix := range []string{"yuv420p", "yuv420p10le"} {
		t.Run(pix, func(t *testing.T) {
			dir := t.TempDir()
			src := richFile(t, dir, pix)
			before := probe(t, src)
			st, id := realJob(t, src, before.DurationSec)
			if err := realWorker(st, media.OSExec{}).run(t); err != nil {
				t.Fatal(err)
			}
			j, _ := st.Get(id)
			if j.Status != jobs.Done {
				t.Fatalf("status %s: %s", j.Status, j.Error)
			}
			after := probe(t, src)
			v := after.Video()
			if v.Codec != "hevc" || v.BitDepth != before.Video().BitDepth {
				t.Errorf("video %+v", v)
			}
			if len(after.Audio()) != 2 || len(after.Subtitles()) != 2 || len(after.Attachments()) != 1 || after.Chapters != 2 {
				t.Errorf("streams lost: audio %d subs %d att %d chapters %d", len(after.Audio()), len(after.Subtitles()), len(after.Attachments()), after.Chapters)
			}
			if _, err := os.Stat(TempPath(src)); err == nil {
				t.Error("temp file left")
			}
		})
	}
}

// run is a tiny helper so tests read well.
func (w *Worker) run(t *testing.T) error {
	t.Helper()
	_, err := w.RunOne(context.Background())
	return err
}

// dropAudioExec runs real ffmpeg but makes the encode lose the second audio track, the way a bad
// mapping would.
type dropAudioExec struct{ media.OSExec }

func (d dropAudioExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for i, a := range args {
		if a == "-map" && i+1 < len(args) && args[i+1] == "-0:d" {
			args = append(append(append([]string{}, args[:i+2]...), "-map", "-0:a:1"), args[i+2:]...)
			break
		}
	}
	return d.OSExec.Stream(ctx, onLine, name, args...)
}

func TestRealEncodeThatDropsAnAudioTrackIsRejected(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := richFile(t, dir, "yuv420p")
	orig, _ := os.ReadFile(src)
	st, id := realJob(t, src, probe(t, src).DurationSec)
	realWorker(st, dropAudioExec{}).run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "audio") {
		t.Fatalf("a lost audio track must be refused: %s %q", j.Status, j.Error)
	}
	if now, _ := os.ReadFile(src); string(now) != string(orig) {
		t.Error("the original changed")
	}
	if _, err := os.Stat(TempPath(src)); err == nil {
		t.Error("temp file left")
	}
}

func TestRealQualityCheckSeparatesGoodFromTerribleAtTheDefaultThreshold(t *testing.T) {
	needTools(t)
	for _, c := range []struct {
		crf  int
		pass bool
	}{{16, true}, {51, false}} {
		dir := t.TempDir()
		src := richFile(t, dir, "yuv420p")
		orig, _ := os.ReadFile(src)
		st, id := realJob(t, src, probe(t, src).DurationSec)
		w := realWorker(st, media.OSExec{})
		w.Rule.CRF = c.crf
		w.MinSSIM = 0 // the default, 0.95
		w.run(t)
		j, _ := st.Get(id)
		if c.pass {
			if j.Status != jobs.Done {
				t.Errorf("CRF %d should pass the default quality check: %s %q", c.crf, j.Status, j.Error)
			}
			continue
		}
		if j.Status != jobs.Kept || !strings.Contains(j.Error, "too different") {
			t.Errorf("CRF %d must be refused: %s %q", c.crf, j.Status, j.Error)
		}
		if now, _ := os.ReadFile(src); string(now) != string(orig) {
			t.Error("the original changed")
		}
	}
}

func TestRealPlainFileAtDefaultThresholdPasses(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "plain.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=320x180:rate=25", "-c:v", "libx264", "-b:v", "4M", "-pix_fmt", "yuv420p", src)
	st, id := realJob(t, src, 6)
	w := realWorker(st, media.OSExec{})
	w.Rule.CRF = 24
	w.MinSSIM = 0 // default threshold
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
}

func TestRealCorruptSourceIsKeptNotReplaced(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := richFile(t, dir, "yuv420p")
	data, _ := os.ReadFile(src)
	os.WriteFile(src, data[:len(data)/2], 0o644) // a truncated original
	orig, _ := os.ReadFile(src)
	st, id := realJob(t, src, 8)
	realWorker(st, media.OSExec{}).run(t)
	j, _ := st.Get(id)
	if j.Status == jobs.Done {
		t.Fatalf("a damaged original must never be 'done': %q", j.Error)
	}
	if now, _ := os.ReadFile(src); string(now) != string(orig) {
		t.Error("the original changed")
	}
	if _, err := os.Stat(TempPath(src)); err == nil {
		t.Error("temp file left")
	}
}

// A long file: segments after the first are where the encoder's start delay used to make a good
// conversion score 0.93 and be refused at the default threshold. mkv, mp4 and ts, default thresholds.
func TestRealLongFilesPassTheDefaultPictureCheckInEveryContainer(t *testing.T) {
	needTools(t)
	if testing.Short() {
		t.Skip("slow: encodes 70 seconds of 720p three times")
	}
	dir := t.TempDir()
	mkv := filepath.Join(dir, "a.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=70", "-f", "lavfi", "-i", "sine=duration=70",
		"-c:v", "libx264", "-b:v", "8M", "-c:a", "aac", mkv)
	files := []string{mkv}
	for _, ext := range []string{"mp4", "ts"} {
		out := filepath.Join(dir, "b."+ext)
		runFFmpeg(t, "-i", mkv, "-c", "copy", out)
		files = append(files, out)
	}
	for _, f := range files {
		t.Run(filepath.Ext(f), func(t *testing.T) {
			st, id := realJob(t, f, 70)
			w := realWorker(st, media.OSExec{})
			w.Rule.CRF = 26
			if err := w.run(t); err != nil {
				t.Fatal(err)
			}
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("a good conversion of a long %s file was refused: %s %q", filepath.Ext(f), j.Status, j.Error)
			}
		})
	}
}

// An original that is itself damaged must not be converted (and so replaced).
func TestRealDamagedOriginalIsKeptNotConverted(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ts := filepath.Join(dir, "clip.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-f", "mpegts", ts)
	data, _ := os.ReadFile(ts)
	for i := len(data) / 3; i < len(data)/3+20000 && i < len(data); i++ { // wreck a stretch in the middle
		data[i] = byte(i * 7)
	}
	os.WriteFile(ts, data, 0o644)
	orig, _ := os.ReadFile(ts)
	st, id := realJob(t, ts, 20)
	realWorker(st, media.OSExec{}).run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "the original file has errors") {
		t.Fatalf("a damaged original must be kept, not converted: %s %q", j.Status, j.Error)
	}
	if now, _ := os.ReadFile(ts); string(now) != string(orig) {
		t.Error("the original changed")
	}
	if _, err := os.Stat(TempPath(ts)); err == nil {
		t.Error("temp file left")
	}
}

// A broadcast recording that begins in the middle of a group of pictures prints harmless messages at
// its start; that must not stop a good file from being converted.
func TestRealRecordingStartingMidStreamIsStillConverted(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "full.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-g", "100", "-f", "mpegts", full)
	data, _ := os.ReadFile(full)
	cut := filepath.Join(dir, "cut.ts")
	os.WriteFile(cut, data[188*400:], 0o644) // start on a packet boundary, but not at a keyframe
	info, err := media.Probe(context.Background(), media.OSExec{}, "ffprobe", cut)
	if err != nil {
		t.Skipf("the cut file is not readable here: %v", err)
	}
	st, id := realJob(t, cut, info.DurationSec)
	realWorker(st, media.OSExec{}).run(t)
	j, _ := st.Get(id)
	t.Logf("status %s %q", j.Status, j.Error)
	if j.Status == jobs.Kept && strings.Contains(j.Error, "the original file has errors") {
		t.Errorf("harmless start-of-stream messages were taken for damage: %q", j.Error)
	}
}
