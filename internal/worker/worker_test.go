// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// ---- a scripted fake ffmpeg/ffprobe ----

// spec describes a file for the fake ffprobe.
type spec struct {
	duration float64
	chapters int
	streams  []map[string]any
}

func vid(codec string, w, h int, pix string) map[string]any {
	return map[string]any{"codec_type": "video", "codec_name": codec, "width": w, "height": h, "pix_fmt": pix,
		"r_frame_rate": "25/1", "avg_frame_rate": "25/1", "disposition": map[string]any{}}
}
func aud(codec, lang string, ch int) map[string]any {
	m := map[string]any{"codec_type": "audio", "codec_name": codec, "channels": ch, "sample_rate": "48000", "disposition": map[string]any{}}
	if lang != "" {
		m["tags"] = map[string]any{"language": lang}
	}
	return m
}
func sub(codec, lang string, forced bool) map[string]any {
	f := 0
	if forced {
		f = 1
	}
	m := map[string]any{"codec_type": "subtitle", "codec_name": codec, "disposition": map[string]any{"forced": f}}
	if lang != "" {
		m["tags"] = map[string]any{"language": lang}
	}
	return m
}
func att(name string) map[string]any {
	return map[string]any{"codec_type": "attachment", "codec_name": "ttf", "tags": map[string]any{"filename": name}}
}

func (s spec) json() string {
	chapters := make([]any, s.chapters)
	for i := range chapters {
		chapters[i] = map[string]any{"id": i}
	}
	b, _ := json.Marshal(map[string]any{
		"streams":  s.streams,
		"chapters": chapters,
		"format":   map[string]any{"format_name": "matroska,webm", "duration": fmt.Sprint(s.duration), "size": "14"},
	})
	return string(b)
}

// sourceSpec is the original file in most tests: H.264 video, English stereo audio, English subtitles,
// a font attachment, two chapters, 100 seconds.
func sourceSpec() spec {
	return spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
}

// goodOutput is what a correct conversion of sourceSpec looks like.
func goodOutput() spec {
	s := sourceSpec()
	s.streams = []map[string]any{vid("hevc", 640, 360, "yuv420p"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}
	return s
}

type scenario struct {
	t     *testing.T
	store *jobs.Store
	dir   string
	src   string
	id    int64

	srcSpec, outSpec       spec
	outProbeRaw            string // if set, the new file's ffprobe answer is exactly this (to test garbage)
	srcProbeFails          bool
	srcPackets, outPackets int
	content                string // what the fake encoder writes
	encodeErr              error
	encodeStderr           string                                // what ffmpeg prints while converting (decode errors in the original show up here)
	hang                   func(name string, args []string) bool // if it returns true the call blocks until its context ends
	decodeStderr           string
	decodeErr              error
	hasSSIM                bool
	ssimStderr             string
	ssimErr                error
	freeBytes              uint64
	freeErr                error
	onEncode               func() // runs while "encoding", e.g. to modify the source
	afterChecks            func() // runs at the picture comparison, the last check

	calls    []string
	exec     *media.FakeExec
	moreSrcs map[string]bool // other originals in the queue (probed like src)
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	os.WriteFile(src, []byte("original-bytes"), 0o644)
	id, _ := st.Enqueue(jobs.Job{Library: dir, Path: src, DurationSec: 100, SizeBefore: 14})
	s := &scenario{
		t: t, store: st, dir: dir, src: src, id: id,
		srcSpec: sourceSpec(), outSpec: goodOutput(),
		srcPackets: 2500, outPackets: 2500, content: "new",
		hasSSIM: true, ssimStderr: "[Parsed_ssim_0] SSIM Y:0.99 U:0.99 V:0.99 All:0.990000 (20.0)",
		freeBytes: 1 << 40,
	}
	s.exec = &media.FakeExec{Handler: s.handle, Block: func(name string, args []string) bool { return s.hang != nil && s.hang(name, args) }}
	return s
}

func (s *scenario) has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func (s *scenario) handle(name string, args []string) (string, string, error) {
	s.calls = append(s.calls, name+" "+strings.Join(args, " "))
	last := args[len(args)-1]
	switch name {
	case "ffprobe":
		isSrc := last == s.src || s.moreSrcs[last]
		if s.has(args, "-count_packets") {
			if isSrc {
				return fmt.Sprint(s.srcPackets), "", nil
			}
			return fmt.Sprint(s.outPackets), "", nil
		}
		if isSrc {
			if s.srcProbeFails {
				return "", "", errors.New("ffprobe: moov atom not found")
			}
			return s.srcSpec.json(), "", nil
		}
		if s.outProbeRaw != "" {
			return s.outProbeRaw, "", nil
		}
		return s.outSpec.json(), "", nil
	case "ffmpeg":
		switch {
		case s.has(args, "libx265"):
			if s.onEncode != nil {
				s.onEncode()
			}
			if s.encodeErr != nil {
				os.WriteFile(last, []byte("partial"), 0o644)
				return "out_time_us=50000000\n", "", s.encodeErr
			}
			os.WriteFile(last, []byte(s.content), 0o644)
			return "out_time_us=50000000\n", s.encodeStderr, nil
		case s.has(args, "-filters"):
			if s.hasSSIM {
				return " T.C ssim              VV->V      Calculate the SSIM between two video streams.\n", "", nil
			}
			return " ... psnr              VV->V      Calculate the PSNR.\n", "", nil
		case s.has(args, "-xerror"):
			return "", s.decodeStderr, s.decodeErr
		case s.has(args, "-lavfi"):
			if s.afterChecks != nil {
				s.afterChecks()
				s.afterChecks = nil
			}
			return "", s.ssimStderr, s.ssimErr
		}
	}
	return "", "", fmt.Errorf("unexpected call: %s %v", name, args)
}

func (s *scenario) worker(mode OutputMode) *Worker {
	return &Worker{ID: "t", Queue: s.store, Exec: s.exec, FFmpeg: "ffmpeg", FFprobe: "ffprobe",
		Rule: rules.Default(), Mode: mode, FreeSpace: func(string) (uint64, error) { return s.freeBytes, s.freeErr }}
}

func (s *scenario) run(mode OutputMode) error {
	_, err := s.worker(mode).RunOne(context.Background())
	return err
}

func (s *scenario) job() jobs.Job {
	j, err := s.store.Get(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	return j
}

func (s *scenario) count(substr string) int {
	n := 0
	for _, c := range s.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// assertOriginalSafe is what every rejection must leave behind.
func (s *scenario) assertOriginalSafe() {
	s.t.Helper()
	if b, err := os.ReadFile(s.src); err != nil || string(b) != "original-bytes" {
		s.t.Errorf("the original was touched: %q %v", b, err)
	}
	if tempLeft(s.src) {
		s.t.Error("a temp file was left behind")
	}
	if _, err := os.Stat(BesidePath(s.src)); err == nil {
		s.t.Error("an output file appeared beside the original")
	}
}

// assertKept: the job is "kept original" with a reason containing want, and the original is safe.
func (s *scenario) assertKept(want string) {
	s.t.Helper()
	j := s.job()
	if j.Status != jobs.Kept {
		s.t.Errorf("status %s (error %q), want kept", j.Status, j.Error)
	}
	if !strings.Contains(strings.ToLower(j.Error), strings.ToLower(want)) {
		s.t.Errorf("reason %q should mention %q", j.Error, want)
	}
	s.assertOriginalSafe()
}

// assertPlain: a detail shown in the page says what happened in plain words (COPY-1): no file paths,
// no tool names, no raw error text such as "exit status".
func assertPlain(t *testing.T, detail string) {
	t.Helper()
	if detail == "" {
		t.Error("no detail")
	}
	for _, bad := range []string{"/", "\\", "exit status", "ffmpeg", "ffprobe", "libx265", "x265", "encoder", "@ 0x", "%!", "<nil>", "syscall", "errno", "no such file"} {
		if strings.Contains(strings.ToLower(detail), bad) {
			t.Errorf("detail %q contains %q", detail, bad)
		}
	}
}

// ---- success ----

func TestReplaceSuccess(t *testing.T) {
	s := newScenario(t)
	if err := s.run(Replace); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "new" {
		t.Errorf("original should be replaced, has %q", b)
	}
	if tempLeft(s.src) {
		t.Error("temp file should be gone")
	}
	j := s.job()
	if j.Status != jobs.Done || j.SizeAfter != 3 || j.Progress != 100 {
		t.Errorf("%+v", j)
	}
	// Every safety check actually ran.
	for _, want := range []string{"-count_packets", "-xerror", "ssim"} {
		if s.count(want) == 0 {
			t.Errorf("no call containing %q", want)
		}
	}
}

func TestBesideKeepsOriginal(t *testing.T) {
	s := newScenario(t)
	if err := s.run(Beside); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Errorf("original changed: %q", b)
	}
	if b, _ := os.ReadFile(BesidePath(s.src)); string(b) != "new" {
		t.Errorf("output missing: %q", b)
	}
	if s.job().Status != jobs.Done {
		t.Errorf("%+v", s.job())
	}
}

func TestPermissionsKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	for _, perm := range []os.FileMode{0o640, 0o444} {
		s := newScenario(t)
		os.Chmod(s.src, perm)
		s.run(Replace)
		if j := s.job(); j.Status != jobs.Done {
			t.Errorf("%v: %s %q", perm, j.Status, j.Error)
		}
		if st, _ := os.Stat(s.src); st.Mode().Perm() != perm {
			t.Errorf("perm %v, want %v", st.Mode().Perm(), perm)
		}
	}
}

// ---- every rejection keeps the original ----

func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *scenario)
		reason string // a phrase the plain-words reason must contain
	}{
		{"truncated: far too short", func(s *scenario) { s.outSpec.duration = 40 }, "long but the original"},
		{"no readable duration", func(s *scenario) { s.outSpec.duration = 0 }, "no readable duration"},
		{"frames dropped", func(s *scenario) { s.outPackets = 1800 }, "video frames"},
		{"audio track missing", func(s *scenario) {
			s.srcSpec.streams = append(s.srcSpec.streams, aud("ac3", "jpn", 6))
			s.outSpec.streams = goodOutput().streams
		}, "audio"},
		{"subtitle missing", func(s *scenario) {
			s.outSpec.streams = []map[string]any{vid("hevc", 640, 360, "yuv420p"), aud("aac", "eng", 2), att("font.ttf")}
		}, "subtitle"},
		{"attachment missing", func(s *scenario) {
			s.outSpec.streams = []map[string]any{vid("hevc", 640, 360, "yuv420p"), aud("aac", "eng", 2), sub("subrip", "eng", false)}
		}, "attachment"},
		{"extra stream", func(s *scenario) {
			s.outSpec.streams = append(goodOutput().streams, aud("aac", "eng", 2))
		}, "audio"},
		{"resolution changed", func(s *scenario) {
			s.outSpec.streams[0] = vid("hevc", 320, 180, "yuv420p")
		}, "picture size"},
		{"colour depth lost", func(s *scenario) {
			s.srcSpec.streams[0] = vid("h264", 640, 360, "yuv420p10le")
			s.outSpec.streams[0] = vid("hevc", 640, 360, "yuv420p")
		}, "colour depth"},
		{"video not hevc", func(s *scenario) { s.outSpec.streams[0] = vid("h264", 640, 360, "yuv420p") }, "format"},
		{"audio language changed", func(s *scenario) { s.outSpec.streams[1] = aud("aac", "und", 2) }, "language"},
		{"audio channels changed", func(s *scenario) { s.outSpec.streams[1] = aud("aac", "eng", 1) }, "channel"},
		{"subtitle turned into another kind", func(s *scenario) { s.outSpec.streams[2] = sub("hdmv_pgs_subtitle", "eng", false) }, "kind"},
		{"forced flag lost", func(s *scenario) {
			s.srcSpec.streams[2] = sub("subrip", "eng", true)
		}, "forced"},
		{"chapters lost", func(s *scenario) { s.outSpec.chapters = 0 }, "chapters"},
		{"zero-byte output", func(s *scenario) { s.content = "" }, "empty"},
		{"new file unreadable", func(s *scenario) { s.outProbeRaw = "this is not json" }, "cannot be read"},
		{"bigger than the original", func(s *scenario) { s.content = "this output is much longer than the original bytes" }, "not smaller"},
		{"decode errors", func(s *scenario) {
			s.decodeStderr = "[hevc @ 0x1] Invalid NAL unit size\nError while decoding stream #0:0"
		}, "does not play cleanly"},
		{"decode exits non-zero", func(s *scenario) { s.decodeErr = errors.New("exit status 69") }, "does not play cleanly"},
		{"picture too different", func(s *scenario) {
			s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
		}, "too different"},
		{"ssim filter missing", func(s *scenario) { s.hasSSIM = false }, "cannot compare picture quality"},
		{"ssim gives no result", func(s *scenario) { s.ssimStderr = "nothing useful" }, "gave no result"},
		{"ssim run fails", func(s *scenario) { s.ssimErr = errors.New("exit status 1") }, "picture comparison at"},
		{"not enough disk space", func(s *scenario) { s.freeBytes = 1 << 20 }, "not enough free disk space"},
		{"cannot check disk space", func(s *scenario) { s.freeErr = errors.New("no such device") }, "free disk space"},
		{"source cannot be read", func(s *scenario) { s.srcProbeFails = true }, "can't be read any more"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			c.mutate(s)
			s.run(Replace)
			s.assertKept(c.reason)
			assertPlain(t, s.job().Error)
		})
	}
}

func TestPreflightRunsBeforeAnyEncoding(t *testing.T) {
	s := newScenario(t)
	s.freeBytes = 1 << 20
	s.run(Replace)
	s.assertKept("not enough free disk space")
	if s.count("libx265") != 0 {
		t.Error("ffmpeg must not even start when the disk is too full")
	}
}

func TestCheapChecksRunBeforeExpensiveOnes(t *testing.T) {
	s := newScenario(t)
	s.outSpec.streams = goodOutput().streams[:2] // subtitle and attachment missing
	s.run(Replace)
	s.assertKept("subtitle")
	if s.count("-xerror") != 0 || s.count("-lavfi") != 0 {
		t.Error("the playback test and picture comparison are only for files that already look right")
	}
}

func TestRejectionReasonNamesEveryProblem(t *testing.T) {
	s := newScenario(t)
	s.outSpec.duration = 40
	s.outSpec.chapters = 0
	s.run(Replace)
	j := s.job()
	if !strings.Contains(j.Error, "chapters") || !strings.Contains(j.Error, "long but the original") {
		t.Errorf("both problems should be listed: %q", j.Error)
	}
}

func TestFFmpegFailureIsFailedNotKept(t *testing.T) {
	s := newScenario(t)
	s.encodeErr = errors.New("ffmpeg exited 1")
	err := s.run(Replace)
	if err == nil {
		t.Fatal("want failure")
	}
	j := s.job()
	if j.Status != jobs.Failed || j.Error == "" {
		t.Errorf("%+v", j)
	}
	s.assertOriginalSafe()
}

func TestMissingSourceFails(t *testing.T) {
	s := newScenario(t)
	os.Remove(s.src)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Failed {
		t.Errorf("%+v", j)
	}
}

func TestSourceChangedWhileEncodingIsKept(t *testing.T) {
	s := newScenario(t)
	s.onEncode = func() { os.WriteFile(s.src, []byte("user replaced this file meanwhile"), 0o644) }
	s.run(Replace)
	j := s.job()
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "changed") {
		t.Errorf("%+v", j)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "user replaced this file meanwhile" {
		t.Errorf("newer file clobbered: %q", b)
	}
	if tempLeft(s.src) {
		t.Error("temp left behind")
	}
}

func TestShutdownRequeuesInsteadOfFailing(t *testing.T) {
	s := newScenario(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.onEncode = cancel
	s.encodeErr = errors.New("signal: killed")
	s.worker(Replace).RunOne(ctx)
	if j := s.job(); j.Status != jobs.Queued {
		t.Errorf("want queued after shutdown, got %+v", j)
	}
	s.assertOriginalSafe()
}

func TestNoJob(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.RunOne(context.Background())
	if ran, err := w.RunOne(context.Background()); ran || err != nil {
		t.Errorf("%v %v", ran, err)
	}
}

type recQueue struct {
	Queue
	progress []float64
}

func (r *recQueue) Progress(id int64, p float64) error {
	r.progress = append(r.progress, p)
	return r.Queue.Progress(id, p)
}

func TestProgressReported(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	rq := &recQueue{Queue: s.store}
	w.Queue = rq
	w.RunOne(context.Background())
	if len(rq.progress) != 1 || rq.progress[0] != 50 {
		t.Errorf("want [50] (50s of 100s), got %v", rq.progress)
	}
}

// ---- the playback test modes ----

func TestDecodeModes(t *testing.T) {
	for _, c := range []struct {
		name     string
		mode     string
		duration float64
		want     int // how many playback runs
		sampled  bool
	}{
		{"auto, short file: full", "auto", 100, 1, false},
		{"default mode is auto", "", 100, 1, false},
		{"auto, long file: three samples", "auto", 5 * 3600, 3, true},
		{"always: full even when long", "always", 5 * 3600, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			s.srcSpec.duration, s.outSpec.duration = c.duration, c.duration
			w := s.worker(Replace)
			w.DecodeCheck = c.mode
			w.RunOne(context.Background())
			if got := s.count("-xerror"); got != c.want {
				t.Errorf("playback runs: %d, want %d", got, c.want)
			}
			if sampled := s.count("-xerror") > 0 && strings.Contains(strings.Join(s.calls, "\n"), "-xerror -nostdin -ss"); sampled != c.sampled {
				t.Errorf("sampled=%v, want %v", sampled, c.sampled)
			}
			if j := s.job(); j.Status != jobs.Done {
				t.Errorf("%+v", j)
			}
		})
	}
}

func TestSampleSegments(t *testing.T) {
	if got := sampleSegments(0); len(got) != 1 || got[0].start != 0 {
		t.Errorf("unknown length: %v", got)
	}
	if got := sampleSegments(12); len(got) != 1 || got[0].length != 12 {
		t.Errorf("short file is compared whole: %v", got)
	}
	got := sampleSegments(3600)
	if len(got) != 3 || got[0].start != 0 || got[1].start != 1795 || got[2].start != 3585 {
		t.Errorf("start, middle, end: %v", got)
	}
	for _, g := range got {
		if g.start < 0 || g.start+g.length > 3600 {
			t.Errorf("segment outside the file: %v", g)
		}
	}
	// the smallest file that gets three samples still has them inside the file
	for _, g := range sampleSegments(30) {
		if g.start < 0 || g.start+g.length > 30 {
			t.Errorf("segment outside a 30s file: %v", g)
		}
	}
}

func TestSSIMThresholdIsConfigurable(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "SSIM Y:0.9 U:0.9 V:0.9 All:0.900000 (10.0)"
	w := s.worker(Replace)
	w.MinSSIM = 0.85
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("0.90 passes a 0.85 threshold: %+v", j)
	}
	s2 := newScenario(t)
	s2.ssimStderr = "SSIM Y:0.9 U:0.9 V:0.9 All:0.900000 (10.0)"
	s2.run(Replace) // default threshold 0.95
	s2.assertKept("too different")
}

func TestSSIMWorstSegmentDecides(t *testing.T) {
	s := newScenario(t)
	n := 0
	good := "SSIM Y:1 U:1 V:1 All:0.990000 (20.0)"
	bad := "SSIM Y:1 U:1 V:1 All:0.700000 (5.0)"
	inner := s.exec.Handler
	s.exec.Handler = func(name string, args []string) (string, string, error) {
		if name == "ffmpeg" && s.has(args, "-lavfi") {
			s.calls = append(s.calls, name+" "+strings.Join(args, " "))
			n++
			if n >= 3 { // the sample at the end of the file is bad at every alignment tried
				return "", bad, nil
			}
			return "", good, nil
		}
		return inner(name, args)
	}
	s.run(Replace)
	s.assertKept("0.700")
	if n != 5 { // two good samples, then the bad one tried at three alignments
		t.Errorf("expected 2 + 3 comparisons, got %d", n)
	}
}

func TestSSIMFilterAskedOnce(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.RunOne(context.Background())
	id2, _ := s.store.Enqueue(jobs.Job{Library: s.dir, Path: s.src, DurationSec: 100})
	_ = id2
	os.WriteFile(s.src, []byte("original-bytes"), 0o644)
	w.RunOne(context.Background())
	if s.count("-filters") != 1 {
		t.Errorf("the filter list should be asked once, was %d times", s.count("-filters"))
	}
}

func TestSSIMComparesTheMainVideoNotCoverArt(t *testing.T) {
	s := newScenario(t)
	cover := vid("mjpeg", 300, 300, "yuvj420p")
	cover["disposition"] = map[string]any{"attached_pic": 1}
	// the cover art is listed first in both files; the main video is the second video stream
	s.srcSpec.streams = []map[string]any{cover, vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2)}
	out := []map[string]any{vid("hevc", 300, 300, "yuv420p"), vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2)}
	out[0]["disposition"] = map[string]any{"attached_pic": 1}
	s.outSpec.streams = out
	s.srcSpec.chapters, s.outSpec.chapters = 0, 0
	s.run(Replace)
	// (the default plan re-encodes the FIRST video stream, so this synthetic output has an h264 main
	// video and is correctly refused; what matters here is which stream the comparison would use)
	joined := strings.Join(s.calls, "\n")
	if s.count("-lavfi") > 0 && (!strings.Contains(joined, "[0:v:1]setpts=") || !strings.Contains(joined, "[1:v:1]setpts=")) {
		t.Errorf("the comparison must use the main video (v:1) of both files, calls:\n%s", joined)
	}
}

// ---- picture comparison alignment ----

var setptsRe = regexp.MustCompile(`setpts=PTS-\((-?[0-9.]+)\)/TB`)

// ssimAligned wraps the handler: the comparison scores 0.93 unless the new file's timeline is shifted
// exactly delta seconds against the source's (what a correct alignment of a delayed encoder does),
// then 0.99.
func ssimAligned(s *scenario, delta float64) {
	inner := s.exec.Handler
	s.exec.Handler = func(name string, args []string) (string, string, error) {
		if name == "ffmpeg" && s.has(args, "-lavfi") {
			s.calls = append(s.calls, name+" "+strings.Join(args, " "))
			var offs []float64
			for _, m := range setptsRe.FindAllStringSubmatch(strings.Join(args, " "), -1) {
				v, _ := strconv.ParseFloat(m[1], 64)
				offs = append(offs, v)
			}
			if len(offs) == 2 && math.Abs(offs[1]-offs[0]-delta) < 0.0005 {
				return "", "SSIM Y:1 U:1 V:1 All:0.990000 (20.0)", nil
			}
			return "", "SSIM Y:1 U:1 V:1 All:0.930000 (11.0)", nil
		}
		return inner(name, args)
	}
}

func TestSSIMAlignsTheNewFileByTheEncodersStartDelay(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["start_time"] = "0.023"
	s.outSpec.streams[0]["start_time"] = "0.040" // the encoder delays the first frame by 17 ms more
	s.srcSpec.duration, s.outSpec.duration = 110, 110
	ssimAligned(s, 0.017)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("a good conversion must pass when aligned by the start-time difference: %+v", j)
	}
	joined := strings.Join(s.calls, "\n")
	for _, want := range []string{
		"-noaccurate_seek -ss 38.000 -t 23.000 -i", // both read from 12 s before the middle sample
		"[0:v:0]setpts=PTS-(0.023000)/TB,trim=start=50.000:duration=10.000[a]",
		"[1:v:0]setpts=PTS-(0.040000)/TB,trim=start=50.000:duration=10.000[b]",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestSSIMTriesOneFrameEitherWayWhenTheStartTimesLie(t *testing.T) {
	s := newScenario(t)
	// start times say "no difference", but the good alignment is one frame (1/25 s) later
	ssimAligned(s, 0.04)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("one frame of misalignment must be forgiven by the retry: %+v", j)
	}
}

func TestSSIMGenuinelyBadStaysBadAtEveryAlignment(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "SSIM Y:1 U:1 V:1 All:0.700000 (5.0)"
	s.run(Replace)
	s.assertKept("too different")
	if s.count("-lavfi") != 3 {
		t.Errorf("the first sample is tried at 3 alignments and the file refused right there, was %d runs", s.count("-lavfi"))
	}
}

// ---- damage in the original, seen while converting ----

func TestOriginalWithDecodeErrorsIsNotConverted(t *testing.T) {
	for _, stderr := range []string{
		"[h264 @ 0x55] error while decoding MB 41 22, bytestream -5",
		"[h264 @ 0x55] concealing 120 DC, 120 AC, 120 MV errors in P frame",
		"[matroska,webm @ 0x1] Invalid data found when processing input",
		"[mov,mp4 @ 0x1] moov atom not found",
	} {
		s := newScenario(t)
		s.encodeStderr = stderr
		s.run(Replace)
		s.assertKept("the original file has errors")
		if s.count("-xerror") != 0 {
			t.Error("no point verifying a conversion of a damaged original")
		}
	}
}

func TestBenignMessagesDoNotRefuseAGoodFile(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = "[h264 @ 0x55] non-existing PPS 0 referenced\n[h264 @ 0x55] decode_slice_header error\n[h264 @ 0x55] no frame!\nx265 [info]: HEVC encoder version 4.1\nx265 [warning]: halving the quality when psy-rd is enabled for 444 input\n[h264 @ 0x55] Last message repeated 4 times"
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("harmless start-of-stream messages must not refuse a good file: %+v", j)
	}
}

func TestSourceDamageClassifier(t *testing.T) {
	if sourceDamage("") != "" || sourceDamage("\n  \n") != "" {
		t.Error("nothing printed means nothing wrong")
	}
	got := sourceDamage("noise\n[h264 @ 1] error while decoding MB 1 2\n[h264 @ 1] Last message repeated 3 times\n[h264 @ 1] concealing 5 DC")
	if !strings.Contains(got, "error while decoding") || !strings.Contains(got, "concealing") || strings.Contains(got, "repeated") {
		t.Errorf("%q", got)
	}
}

// ---- order and time limits ----

func TestSourceChangeIsReportedBeforeTheExpensiveChecks(t *testing.T) {
	s := newScenario(t)
	s.onEncode = func() { os.WriteFile(s.src, []byte("user replaced this file meanwhile"), 0o644) }
	s.outSpec.streams = goodOutput().streams[:2] // would also fail parity
	s.run(Replace)
	j := s.job()
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "changed while") || strings.Contains(j.Error, "subtitle") {
		t.Errorf("the change must be reported first, not a confusing parity message: %+v", j)
	}
	if s.count("-count_packets") > 1 || s.count("-xerror") != 0 {
		t.Errorf("no verification should run after the source changed: %v", s.calls)
	}
}

func withTimeouts(t *testing.T, quick, count, sample time.Duration) {
	t.Helper()
	q, c, sm := quickTimeout, countTimeout, sampleTimeout
	quickTimeout, countTimeout, sampleTimeout = quick, count, sample
	t.Cleanup(func() { quickTimeout, countTimeout, sampleTimeout = q, c, sm })
}

func TestAHungCheckKeepsTheOriginalInsteadOfFreezingTheWorker(t *testing.T) {
	cases := []struct {
		name string
		hang func(name string, args []string) bool
		want string
	}{
		{"probe of the new file", func(n string, a []string) bool {
			return n == "ffprobe" && !strings.Contains(strings.Join(a, " "), "movie.mkv ") && strings.Contains(a[len(a)-1], ".syp-tmp") && !strings.Contains(strings.Join(a, " "), "count_packets")
		}, "reading the new file took too long"},
		{"frame count of the new file", func(n string, a []string) bool {
			return n == "ffprobe" && strings.Contains(strings.Join(a, " "), "count_packets") && strings.Contains(a[len(a)-1], ".syp-tmp")
		}, "counting the new file's video frames took too long"},
		{"playback test", func(n string, a []string) bool {
			return n == "ffmpeg" && strings.Contains(strings.Join(a, " "), "-xerror")
		}, "playback test of the new file took too long"},
		{"picture comparison", func(n string, a []string) bool {
			return n == "ffmpeg" && strings.Contains(strings.Join(a, " "), "-lavfi")
		}, "picture comparison at 0:00:00 took too long"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withTimeouts(t, 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond)
			oldBase, oldCap := fullDecodeBase, fullDecodeCap
			fullDecodeBase, fullDecodeCap = 150*time.Millisecond, 300*time.Millisecond
			t.Cleanup(func() { fullDecodeBase, fullDecodeCap = oldBase, oldCap })
			s := newScenario(t)
			s.hang = c.hang
			start := time.Now()
			s.run(Replace)
			s.assertKept(c.want)
			if time.Since(start) > 10*time.Second {
				t.Error("the worker took far too long to give up")
			}
		})
	}
}

func TestShutdownDuringACheckReleasesTheJob(t *testing.T) {
	s := newScenario(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.hang = func(n string, a []string) bool {
		if n == "ffmpeg" && strings.Contains(strings.Join(a, " "), "-xerror") {
			go cancel() // Ctrl+C while the playback test runs
			return true
		}
		return false
	}
	s.worker(Replace).RunOne(ctx)
	if j := s.job(); j.Status != jobs.Queued {
		t.Errorf("Ctrl+C during a check puts the job back in the queue, not 'kept': %+v", j)
	}
	s.assertOriginalSafe()
}

// Reading the start sample needs no seek: "-ss 0" made an AVI jump to a later keyframe and a good
// conversion fail its start sample. Later samples still seek.
func TestSSIMStartSampleDoesNotSeek(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.duration, s.outSpec.duration = 110, 110
	s.run(Replace)
	var samples []string
	for _, c := range s.calls {
		if strings.Contains(c, "-lavfi") {
			samples = append(samples, c)
		}
	}
	if len(samples) < 2 {
		t.Fatalf("want the start and middle samples compared, got %d calls:\n%s", len(samples), strings.Join(s.calls, "\n"))
	}
	if strings.Contains(samples[0], "-ss") {
		t.Errorf("the start sample must read from the start without -ss:\n%s", samples[0])
	}
	if !strings.Contains(samples[1], "-ss 38.000") {
		t.Errorf("the middle sample still seeks:\n%s", samples[1])
	}
}

// Every way a conversion fails leaves a plain detail (COPY-1/COPY-2): the raw error goes to the log only.
func TestFailedDetailsArePlain(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *scenario)
		want   string
	}{
		{"converter crash", func(s *scenario) {
			s.encodeErr = fmt.Errorf("ffmpeg: exit status 187: %s/x.mkv: Invalid data found", "/media")
		}, "Converting stopped with an error"},
		{"disk full while converting", func(s *scenario) {
			s.encodeErr = fmt.Errorf("ffmpeg: exit status 1: av_interleaved_write_frame(): No space left on device")
		}, "The disk is full"},
		{"disk full, as an error", func(s *scenario) {
			s.encodeErr = &os.PathError{Op: "write", Path: "/media/x.mkv", Err: syscall.ENOSPC}
		}, "The disk is full"},
		{"source gone", func(s *scenario) { os.Remove(s.src) }, "not there any more"},
		{"source cannot be opened for reading", func(s *scenario) {
			os.Chmod(s.src, 0)
			s.t.Cleanup(func() { os.Chmod(s.src, 0o644) })
		}, "not allowed to read this file"},
		{"old temp file cannot be removed", func(s *scenario) { // through the hook: works on every OS
			os.WriteFile(filepath.Join(s.dir, "movie.syp-tmp.mkv"), []byte("x"), 0o644)
			removeFile = func(p string) error { return &os.PathError{Op: "remove", Path: p, Err: syscall.EACCES} }
			s.t.Cleanup(func() { removeFile = os.Remove })
		}, "old temp file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// chmod 0 stops reading only on POSIX, for a user other than root; the words are checked
			// on every OS below (cannotRead)
			if c.name == "source cannot be opened for reading" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("needs POSIX permissions and a user other than root")
			}
			s := newScenario(t)
			c.mutate(s)
			s.run(Replace)
			j := s.job()
			if j.Status != jobs.Failed || !strings.Contains(j.Error, c.want) {
				t.Errorf("%s %q, want failed with %q", j.Status, j.Error, c.want)
			}
			assertPlain(t, j.Error)
		})
	}
	// errors that come from below a stage, by class
	for _, c := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: "/media/a.mkv", Err: syscall.EACCES}, "not allowed to write"},
		{&os.PathError{Op: "write", Path: "/media/a.mkv", Err: syscall.ENOSPC}, "The disk is full"},
		{errors.New("rename /a /b: exit status 1"), "Converting did not finish"},
		{failed("Plain words.", &os.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC}), "Plain words."},
		{failed(cannotRead, &os.PathError{Op: "open", Path: "/media/a.mkv", Err: syscall.EACCES}), "not allowed to read this file"},
	} {
		got := plainFailure(c.err)
		if !strings.Contains(got, c.want) {
			t.Errorf("plainFailure(%v) = %q, want %q", c.err, got, c.want)
		}
		assertPlain(t, got)
	}
	// a check that ran out of time
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	var ke *KeptError
	if err := checkFailed(ctx, "the playback test of the new file", errors.New("signal: killed")); !errors.As(err, &ke) || !strings.Contains(err.Error(), "took too long") {
		t.Errorf("timeout: %v", err)
	} else {
		assertPlain(t, ke.Reason)
	}
}
