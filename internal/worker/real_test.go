// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestRealWorkerRunsAClipToItsResult takes one real job through the worker and flow.Run: a generated H.264 clip
// in a temp library, the shrink-videos starter, one slot. The job ends done with a smaller or kept-bigger result,
// the HEVC output sits beside the clip, and the original is byte-identical. The job source is in memory; the
// store-backed Builtin source is not exercised here.
func TestRealWorkerRunsAClipToItsResult(t *testing.T) {
	requireTools(t)
	root := t.TempDir()
	src := filepath.Join(root, "holiday.mkv")
	mk := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", src)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	id, err := fsx.FileID(src)
	if err != nil {
		t.Fatal(err)
	}
	starter, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	in := flow.RunInput{
		JobID: 1, Attempt: 1, LibraryID: "1", WorkerID: "local", WorkflowID: 1, Revision: 1,
		LibraryRoot: root, Source: src, Size: st.Size(), MTime: st.ModTime(), FileID: id,
	}
	env := flow.Env{
		Media:  media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
		Safety: flow.RealSafety{}, Pick: flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) { return starter.Flow, nil },
		Now:          time.Now, Sleep: time.Sleep, AppVersion: "test",
	}
	src2 := &fakeSource{queue: []Work{{Job: jobs.Job{ID: 1, Attempt: 1}, In: in}}}
	w := New(Config{Source: src2, Run: flow.Run, Env: env, DataDir: t.TempDir(), Tick: 5 * time.Millisecond})
	stop := start(t, w)
	defer stop()
	waitForWithin(t, time.Minute, "the job result", func() bool {
		f, _, _, _ := src2.snapshot()
		return len(f) == 1
	})

	f, _, _, _ := src2.snapshot()
	if f[0].Verdict != jobs.VerdictDone || (f[0].Outcome != "smaller" && f[0].Outcome != "bigger_kept") {
		t.Fatalf("result = %+v; want done with smaller or bigger_kept", f[0])
	}
	after, err := os.ReadFile(src)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the original changed or is gone: %v", err)
	}
	if f[0].Outcome == "smaller" {
		out := filepath.Join(root, "holiday-shrunk.mkv")
		got, err := media.Probe(context.Background(), out)
		if err != nil {
			t.Fatalf("probe the output: %v", err)
		}
		if len(got.Video) != 1 || got.Video[0].Codec != "hevc" {
			t.Fatalf("output video = %+v, want one HEVC track", got.Video)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".syp-tmp") {
			t.Errorf("staging folder %s left behind", e.Name())
		}
	}
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s not found and SYP_REQUIRE_FFMPEG is set", bin)
			}
			t.Skipf("%s not found", bin)
		}
	}
}
