// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
			skipUnlessRequired(t, "%s not installed", tool)
		}
	}
}

// skipUnlessRequired skips a real-ffmpeg test, except where SYP_REQUIRE_FFMPEG=1 (CI's Linux job) says
// these tests must run: there a skip would silently hide them, so it fails instead.
func skipUnlessRequired(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("SYP_REQUIRE_FFMPEG") == "1" {
		t.Fatalf("SYP_REQUIRE_FFMPEG=1 but this real-ffmpeg test cannot run: "+format, args...)
	}
	t.Skipf(format, args...)
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
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
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
	r.MinBitrateKbps = 0 // the scanner decided: these files are queued (the worker honours a Plan's skip, review 6cafc6f)
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
			if tempLeft(src) {
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
	if tempLeft(src) {
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
	if tempLeft(src) {
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

// wreckStretches overwrites size bytes at each of n evenly spaced places in data. Each stretch is a few damaged
// pictures to ffmpeg's decoder, so the count of damaged pictures grows with n.
func wreckStretches(data []byte, n, size int) {
	for s := 0; s < n; s++ {
		start := len(data)*s/n + len(data)/(2*n)
		for i := start; i < start+size && i < len(data); i++ {
			data[i] = byte(i * 7)
		}
	}
}

// An original with more than 1% of its pictures damaged must not be converted (and so replaced). The damage is
// spread over 20 stretches (about 14 damaged pictures of 500: 2.8%). One stretch of 20000 bytes is a single damaged
// picture, which is under 1%: TestRealLightlyDamagedOriginalIsConverted covers that case.
func TestRealDamagedOriginalIsKeptNotConverted(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ts := filepath.Join(dir, "clip.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-f", "mpegts", ts)
	data, _ := os.ReadFile(ts)
	wreckStretches(data, 20, 2000)
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
	if tempLeft(ts) {
		t.Error("temp file left")
	}
}

// An original with under 1% damaged pictures plays through, so it converts: the damage does not stop a good file.
//
// The stretch goes near the end of the stream, where a broken picture cannot desync the rest of it. A stretch in
// the middle did: the decoder printed errors until the next keyframe, and how many depended on the encoder and its
// threading (CI's counted 6+ of 500 from 20000 bytes and kept the file, while the same fixture here counted one).
// Even at the end the count varies, so the stretch is the first of a few that leaves the file damaged but under
// tolerance; if none does, that is said here, not below as if the rule were wrong. decodedDamage counts the way
// the worker does.
func TestRealLightlyDamagedOriginalIsConverted(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ts := filepath.Join(dir, "clip.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-f", "mpegts", ts)
	clean, _ := os.ReadFile(ts)
	var n int
	for _, r := range []struct{ size, gap int }{{2000, 1000}, {4000, 1000}, {6000, 2000}, {8000, 3000}, {1000, 500}} {
		data := append([]byte(nil), clean...)
		for i, hi := len(data)-r.gap-r.size, len(data)-r.gap; i < hi; i++ {
			data[i] = byte(i * 7)
		}
		os.WriteFile(ts, data, 0o644)
		if n = decodedDamage(t, ts); n >= 1 && int64(n)*100 <= 500*maxDamagedPercent {
			break
		}
	}
	if n < 1 || int64(n)*100 > 500*maxDamagedPercent {
		t.Fatalf("no stretch left the file lightly damaged (%d pictures of 500): the fixture cannot be the test's case", n)
	}
	st, id := realJob(t, ts, 20)
	realWorker(st, media.OSExec{}).run(t)
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("a file with under 1%% damage plays through and must convert: %s %q", j.Status, j.Error)
	}
}

// decodedDamage decodes path and counts the damaged pictures the way the worker does (the decoder's own
// messages and the report's decode errors).
func decodedDamage(t *testing.T, path string) int {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "level+verbose", "-nostats", "-i", path, "-f", "null", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("decoding %s: %v\n%s", path, err, stderr.String())
	}
	rep := sourceDamage(stderr.String())
	if st, ok := media.InputStreamStats(stderr.String(), 0, 0); ok && int(st.DecodeErrors) > rep.Pictures {
		rep.Pictures = int(st.DecodeErrors)
	}
	return rep.Pictures
}

// A recording that begins in the middle of a group of pictures has no picture to show in its first seconds, in the
// original as much as in any new file, so it still converts and the job's log says how much undecodable lead was
// left out. The start-of-stream decoder messages (non-existing PPS, no frame) are not damage either.
//
// Without sound the lead also shortens the new file, so the length check must leave it out. With sound, the copied
// track covers the lead and the new file is the original's length: the duration must not be credited there, or a
// good conversion would be refused as too short. Both are recordings cut out of a stream, the ordinary case.
func TestRealRecordingStartingMidStreamIsConverted(t *testing.T) {
	needTools(t)
	for _, sound := range []bool{false, true} {
		name := "picture only"
		if sound {
			name = "with sound"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			full := filepath.Join(dir, "full.ts")
			args := []string{"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20"}
			if sound {
				args = append(args, "-f", "lavfi", "-i", "sine=duration=20", "-c:a", "aac", "-b:a", "128k")
			}
			args = append(args, "-c:v", "libx264", "-b:v", "2M", "-g", "100", "-f", "mpegts", full)
			runFFmpeg(t, args...)
			data, _ := os.ReadFile(full)
			cut := filepath.Join(dir, "cut.ts")
			os.WriteFile(cut, data[188*400:], 0o644) // start on a packet boundary, but not at a keyframe
			info, err := media.Probe(context.Background(), media.OSExec{}, "ffprobe", cut)
			if err != nil {
				skipUnlessRequired(t, "the cut file is not readable here: %v", err)
			}
			st, id := realJob(t, cut, info.DurationSec)
			realWorker(st, media.OSExec{}).run(t)
			j, _ := st.Get(id)
			if j.Status != jobs.Done {
				t.Fatalf("a recording that starts mid-stream must convert, not %s: %q", j.Status, j.Error)
			}
			lines, _ := st.LogLines(id)
			said := false
			for _, l := range lines {
				said = said || strings.HasPrefix(l.Text, "the original starts mid-stream: its first ") &&
					strings.Contains(l.Text, " seconds have no picture that can be decoded")
			}
			if !said {
				t.Errorf("the job log does not say how much undecodable lead was left out: %v", lines)
			}
		})
	}
}

// A recording with a piece missing in the middle is not one that starts mid-stream: only the lead is credited, so
// a gap inside the file is still refused.
func TestRealGapInTheMiddleIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ts := filepath.Join(dir, "clip.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-f", "mpegts", ts)
	data, _ := os.ReadFile(ts)
	at := len(data) / 2 / 188 * 188 // on a packet boundary, so the transport stream stays readable
	gapped := filepath.Join(dir, "gap.ts")
	os.WriteFile(gapped, append(append([]byte(nil), data[:at]...), data[at+188*500:]...), 0o644) // 500 packets cut out
	st, id := realJob(t, gapped, 20)
	realWorker(st, media.OSExec{}).run(t)
	j, _ := st.Get(id)
	if j.Status == jobs.Done {
		t.Fatalf("a recording with a gap in the middle must never be 'done': %q", j.Error)
	}
	if now, _ := os.ReadFile(gapped); len(now) != len(data)-188*500 {
		t.Error("the original changed")
	}
	if tempLeft(gapped) {
		t.Error("temp file left")
	}
}

// A constant-frame-rate AVI (dev-qa2's soak generator, smaller picture): a good conversion was
// refused at the start sample (0.934, the rest 0.994). Asking ffmpeg to seek to 0 in an AVI jumps to
// a later keyframe (8.4 s in the original, 1.4 s in the new file), so the start sample compared the
// wrong stretches; reading from the start needs no seek at all. A .mov copy is checked the same way.
func TestRealCFRAviAndMovPassTheStartSample(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	avi := filepath.Join(dir, "a.avi")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=40:size=640x360:rate=30", "-f", "lavfi", "-i", "sine=duration=40:frequency=440",
		"-c:v", "libx264", "-b:v", "8M", "-pix_fmt", "yuv420p", "-t", "40", "-c:a", "aac", "-b:a", "128k", avi)
	mov := filepath.Join(dir, "b.mov")
	runFFmpeg(t, "-i", avi, "-c", "copy", mov)
	for _, f := range []string{avi, mov} {
		t.Run(filepath.Ext(f), func(t *testing.T) {
			st, id := realJob(t, f, 40)
			w := realWorker(st, media.OSExec{})
			w.Rule.CRF = 26 // the default quality, at the default picture threshold
			if err := w.run(t); err != nil {
				t.Fatal(err)
			}
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("a good conversion of a CFR %s was refused: %s %q", filepath.Ext(f), j.Status, j.Error)
			}
		})
	}
}

// The slider's recipe on real files (slice 16b): what the scanner's Plan decides, and what the worker
// produces, at Balanced.
func TestRealSliderBalanced(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	gen := func(name, size, codec string, kbps int, extra ...string) string {
		p := filepath.Join(dir, name)
		args := []string{"-f", "lavfi", "-i", "testsrc2=duration=2:size=" + size + ":rate=25", "-c:v", codec, "-b:v", fmt.Sprint(kbps) + "k"}
		runFFmpeg(t, append(append(args, extra...), p)...)
		return p
	}
	balanced := rules.SliderTemplate(3)
	convert := func(t *testing.T, path string) string {
		t.Helper()
		if p := balanced.Compile(probe(t, path)); p.Skip != "" {
			t.Fatalf("the scanner would skip it: %s", p.Skip)
		}
		st, id := realJob(t, path, 2)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, int64, error) { return balanced, 0, nil }
		if err := w.run(t); err != nil {
			t.Fatal(err)
		}
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
		b, _ := os.ReadFile(path)
		return regexp.MustCompile(`crf=[0-9.]+`).FindString(string(b)) // x265 writes its settings into the file
	}
	t.Run("1080p H.264 at CRF 23", func(t *testing.T) {
		if crf := convert(t, gen("a1080.mkv", "1920x1080", "libx264", 8000)); crf != "crf=23.0" {
			t.Errorf("got %q", crf)
		}
	})
	t.Run("720p at CRF 22", func(t *testing.T) {
		if crf := convert(t, gen("a720.mkv", "1280x720", "libx264", 5000)); crf != "crf=22.0" {
			t.Errorf("got %q", crf)
		}
	})
	t.Run("10-bit stays 10-bit", func(t *testing.T) {
		p := gen("a10.mkv", "1280x720", "libx264", 5000, "-pix_fmt", "yuv420p10le")
		convert(t, p)
		if v := probe(t, p).Video(); v == nil || v.Codec != "hevc" || v.BitDepth != 10 {
			t.Errorf("%+v", v)
		}
	})
	t.Run("HEVC in MKV with nothing to change is skipped (Basic, S4)", func(t *testing.T) {
		p := gen("h.mkv", "1280x720", "libx265", 5000, "-x265-params", "log-level=error")
		if c := balanced.Compile(probe(t, p)).SkipCode; c != rules.SkipAlreadyInFormat {
			t.Errorf("%q", c)
		}
	})
	t.Run("a small file is converted too (always standardize)", func(t *testing.T) {
		p := gen("small.mkv", "1920x1080", "libx264", 300)
		if s := balanced.Compile(probe(t, p)).Skip; s != "" {
			t.Errorf("%q", s)
		}
	})
}

// A phone video (#81, QA cdcbcb9): its Display Matrix turns it sideways; ffmpeg encodes the picture
// upright, so the new file is 360x640 with no rotation left, plays upright, and passes the checks.
func TestRealRotatedPhoneVideo(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	base, src := filepath.Join(dir, "base.mp4"), filepath.Join(dir, "rot.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=4:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration=4",
		"-c:v", "libx264", "-b:v", "3000k", "-c:a", "aac", base)
	runFFmpeg(t, "-display_rotation", "90", "-i", base, "-c", "copy", src)
	if v := probe(t, src).Video(); v.Rotation == 0 || !v.Quarter() {
		t.Fatalf("the test file has no rotation: %+v", v)
	}
	st, id := realJob(t, src, 4)
	if err := realWorker(st, media.OSExec{}).run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if v := probe(t, src).Video(); v.Codec != "hevc" || v.Width != 360 || v.Height != 640 || v.Rotation != 0 {
		t.Errorf("new file %+v: want 360x640 upright, no rotation left", v)
	}
}

// Review 44dbabe: a rotated phone video with "at most 480" ends 270x480 upright.
func TestRealRotatedVideoCappedUpright(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	base, src := filepath.Join(dir, "base.mp4"), filepath.Join(dir, "rot.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-c:v", "libx264", "-b:v", "2000k", base)
	runFFmpeg(t, "-display_rotation", "90", "-i", base, "-c", "copy", src)
	s := rules.DefaultSelectors(3)
	s.Resolution, s.FileType = "480", "keep" // keep: S6's MKV default would rename rot.mp4
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if v := probe(t, src).Video(); v.Width != 270 || v.Height != 480 || v.Rotation != 0 {
		t.Errorf("new file %+v: want 270x480 upright", v)
	}
}

// A rotated HEVC phone video repackaged by Basic (S4 + #81): the copied stream keeps its size, and the
// new MKV keeps the rotation, so it still plays upright.
func TestRealRotatedHEVCRepackaged(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	base, src := filepath.Join(dir, "base.mp4"), filepath.Join(dir, "rot.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration=3",
		"-c:v", "libx265", "-preset", "ultrafast", "-tag:v", "hvc1", "-x265-params", "log-level=error", "-c:a", "aac", "-ac", "2", base)
	runFFmpeg(t, "-display_rotation", "90", "-i", base, "-c", "copy", src)
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	tmpl := rules.BasicTemplate(3, "keep_all")
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if v := probe(t, j.OutputPath).Video(); v.Width != 640 || v.Height != 360 || !v.Quarter() {
		t.Errorf("new file %+v: want the coded 640x360 with its rotation kept", v)
	}
}

// A stretch can lose frames without a decoder message (470 of 500 decode here, with no error line for the lost 30):
// the lost frames are damage too, so the original is kept.
func TestRealLostFramesOriginalIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	ts := filepath.Join(dir, "clip.ts")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20", "-c:v", "libx264", "-b:v", "2M", "-f", "mpegts", ts)
	data, _ := os.ReadFile(ts)
	for i := len(data) / 3; i < len(data)/3+300000 && i < len(data); i++ {
		data[i] = byte(i * 7)
	}
	os.WriteFile(ts, data, 0o644)
	st, id := realJob(t, ts, 20)
	realWorker(st, media.OSExec{}).run(t)
	if j, _ := st.Get(id); j.Status != jobs.Kept || !strings.Contains(j.Error, "the original file has errors") {
		t.Fatalf("an original that loses frames silently must be kept, not converted: %s %q", j.Status, j.Error)
	}
}

// A clean clip whose audio runs past its video (10 s picture, 11 s sound) is not damaged: its frames are all there,
// and the container's longer duration must not make them look lost.
// An mp4 edit list (a trimmed start, made with -ss and -c copy) is clean: ffmpeg outputs only the presented
// frames, which are fewer than the stream's count, so the trimmed frames are not lost data and it converts.
func TestRealEditListMP4Converts(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.mp4")
	path := filepath.Join(dir, "edit-list.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=10",
		"-f", "lavfi", "-i", "sine=duration=11", "-c:v", "libx264", "-b:v", "2M", "-c:a", "aac", clean)
	runFFmpeg(t, "-ss", "0.4", "-i", clean, "-c", "copy", path)
	st, id := realJob(t, path, 9.6)
	realWorker(st, media.OSExec{}).run(t)
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("a clean edit-list mp4 must convert: %s %q", j.Status, j.Error)
	}
}

func TestRealCleanClipWithLongerAudioConverts(t *testing.T) {
	needTools(t)
	for _, ext := range []string{"mkv", "mp4"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "clip."+ext)
			runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=10",
				"-f", "lavfi", "-i", "sine=duration=11", "-c:v", "libx264", "-b:v", "2M", "-c:a", "aac", path)
			st, id := realJob(t, path, 11)
			realWorker(st, media.OSExec{}).run(t)
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("a clean clip with longer audio must convert: %s %q", j.Status, j.Error)
			}
		})
	}
}
