// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// requireRealTools is the media package's L-INST-7 rule: skip only when a tool is missing and
// SYP_REQUIRE_FFMPEG is unset; CI sets it and fails on any "--- SKIP: TestReal".
func requireRealTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s is not on PATH and SYP_REQUIRE_FFMPEG is set: the real flow test must not skip", tool)
			}
			t.Skipf("%s is not on PATH (set SYP_REQUIRE_FFMPEG=1 to fail instead)", tool)
		}
	}
}

func hasEncoder(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatalf("ffmpeg -encoders: %v", err)
	}
	return strings.Contains(string(out), " "+name+" ")
}

// TestRealRunShrinksAClip walks §7 end to end on a real file: the starter writes an HEVC MKV
// next to a fat H.264 source, the original is untouched byte for byte, and the staging folder is
// gone.
func TestRealRunShrinksAClip(t *testing.T) {
	requireRealTools(t)
	for _, encoder := range []string{"libx264", "libx265"} {
		if !hasEncoder(t, encoder) {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
			}
			t.Skipf("this ffmpeg build has no %s", encoder)
		}
	}

	root := t.TempDir()
	src := filepath.Join(root, "holiday.mkv")
	// A deliberately fat source: testsrc2 at a high bitrate so an HEVC re-encode at CRF 23 is
	// smaller even at this size. Calm 30 fps content, the picture check's ordinary case.
	make := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", src)
	if out, err := make.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read the source: %v", err)
	}
	info, err := media.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	if len(info.Video) != 1 || info.Video[0].Codec != "h264" {
		t.Fatalf("source video = %+v, want one H.264 track", info.Video)
	}
	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	id, err := fsx.FileID(src)
	if err != nil {
		t.Fatal(err)
	}

	in := flow.RunInput{
		JobID: 1, Attempt: 1, LibraryID: "Movies", WorkerID: "local",
		WorkflowID: 1, Revision: 1,
		LibraryRoot: root, Source: src,
		Size: st.Size(), MTime: st.ModTime(), FileID: id,
	}
	env := flow.Env{
		Media:        media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run},
		Safety:       flow.RealSafety{},
		Pick:         flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) { return starterFlow(t), nil },
		Now:          time.Now,
		Sleep:        time.Sleep,
		AppVersion:   "test",
	}
	res := flow.Run(context.Background(), in, env)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	want := filepath.Join(root, "holiday-shrunk.mkv")
	if res.OutputPath != want {
		t.Fatalf("output = %q, want %q", res.OutputPath, want)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("the original is gone: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the original changed: %d -> %d bytes", len(before), len(after))
	}

	got, err := media.Probe(context.Background(), want)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	if len(got.Video) != 1 || got.Video[0].Codec != "hevc" {
		t.Errorf("output video = %+v, want one HEVC track", got.Video)
	}
	if len(got.Audio) != 1 {
		t.Errorf("output audio = %+v, want the copied track", got.Audio)
	}
	if res.SizeAfter >= res.SizeBefore {
		t.Errorf("sizes = %d -> %d, want smaller", res.SizeBefore, res.SizeAfter)
	}
	// Nothing the run staged is left beside the files.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".syp-tmp") {
			t.Errorf("staging folder %s left behind", e.Name())
		}
	}
}

// makeRealClip encodes one real source file with the given ffmpeg input and output arguments.
func makeRealClip(t *testing.T, path string, args ...string) {
	t.Helper()
	argv := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	argv = append(argv, path)
	if out, err := exec.Command("ffmpeg", argv...).CombinedOutput(); err != nil {
		t.Fatalf("make %s: %v\n%s", filepath.Base(path), err, out)
	}
}

// runRealFlow runs the starter on one real file with the audio step's settings changed.
func runRealFlow(t *testing.T, src, audio string) flow.Result {
	t.Helper()
	root := filepath.Dir(src)
	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	id, err := fsx.FileID(src)
	if err != nil {
		t.Fatal(err)
	}
	rev := starterFlow(t)
	rev.Steps[1].Settings = json.RawMessage(audio)
	in := flow.RunInput{
		JobID: 1, Attempt: 1, LibraryID: "Movies", WorkerID: "local",
		WorkflowID: 1, Revision: 1,
		LibraryRoot: root, Source: src,
		Size: st.Size(), MTime: st.ModTime(), FileID: id,
	}
	env := flow.Env{
		Media:        media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run},
		Safety:       flow.RealSafety{},
		Pick:         flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) { return rev, nil },
		Now:          time.Now,
		Sleep:        time.Sleep,
		AppVersion:   "test",
	}
	return flow.Run(context.Background(), in, env)
}

// TestRealRunConvertsSurroundToStereoAAC is L-AUD-6's real path: the plan's "stereo AAC" on a 5.1
// source writes exactly one AAC stream with two channels, read back with ffprobe.
func TestRealRunConvertsSurroundToStereoAAC(t *testing.T) {
	requireRealTools(t)
	for _, encoder := range []string{"libx264", "libx265", "aac", "eac3"} {
		if !hasEncoder(t, encoder) {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
			}
			t.Skipf("this ffmpeg build has no %s", encoder)
		}
	}

	root := t.TempDir()
	src := filepath.Join(root, "surround.mkv")
	makeRealClip(t, src,
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-pix_fmt", "yuv420p",
		"-ac", "6", "-c:a", "eac3", "-shortest")
	before, err := media.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	if len(before.Audio) != 1 || before.Audio[0].Channels != 6 {
		t.Fatalf("source audio = %+v, want one 5.1 track", before.Audio)
	}

	res := runRealFlow(t, src, `{"format":"aac","channels":"stereo"}`)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	got, err := media.Probe(context.Background(), res.OutputPath)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	if len(got.Audio) != 1 {
		t.Fatalf("output audio = %+v, want one track", got.Audio)
	}
	if got.Audio[0].Codec != "aac" || got.Audio[0].Channels != 2 {
		t.Errorf("output audio = %+v, want one stereo AAC track", got.Audio[0])
	}
}

// TestRealRunKeepsTheChosenLanguage is the language filter's real path: "keep English" writes one
// AAC stream, in English, and the German track is gone.
func TestRealRunKeepsTheChosenLanguage(t *testing.T) {
	requireRealTools(t)
	for _, encoder := range []string{"libx264", "libx265", "aac"} {
		if !hasEncoder(t, encoder) {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
			}
			t.Skipf("this ffmpeg build has no %s", encoder)
		}
	}

	root := t.TempDir()
	src := filepath.Join(root, "twolangs.mkv")
	makeRealClip(t, src,
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=4",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=ger")
	before, err := media.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	if len(before.Audio) != 2 {
		t.Fatalf("source audio = %+v, want two tracks", before.Audio)
	}

	res := runRealFlow(t, src, `{"format":"copy","languages":["eng"]}`)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	got, err := media.Probe(context.Background(), res.OutputPath)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	if len(got.Audio) != 1 {
		t.Fatalf("output audio = %+v, want the English track only", got.Audio)
	}
	if got.Audio[0].Codec != "aac" || !strings.EqualFold(got.Audio[0].Language, "eng") {
		t.Errorf("output audio = %+v, want the English AAC track", got.Audio[0])
	}
}
