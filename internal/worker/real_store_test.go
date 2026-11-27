// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestRealStoreBackedJobEndToEnd is the whole path with real tools: a temp store with every migration, a library
// over a folder with a generated clip, the shrink-videos workflow saved, one enqueue, and the worker reading the job
// through the built-in source. The job row ends done, the HEVC output sits beside the clip, the clip is unchanged,
// and no staging folder is left.
func TestRealStoreBackedJobEndToEnd(t *testing.T) {
	requireTools(t)
	ctx := context.Background()
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
	fid, err := fsx.FileID(src)
	if err != nil {
		t.Fatal(err)
	}

	s := newStoredJobFor(t, root, fid, st.Size(), st.ModTime().UnixNano(), src)
	env := flow.Env{
		Media:  media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
		Safety: flow.RealSafety{}, Pick: flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) {
			rev, err := s.flows.Revision(ctx, id, r)
			return rev.Flow, err
		},
		Now: time.Now, Sleep: time.Sleep, AppVersion: "test",
	}
	w := New(Config{
		Slots: 1, WorkerID: "local", DataDir: t.TempDir(), Tick: 5 * time.Millisecond,
		Source: Builtin{Queue: s.q, DB: s.db, Flows: s.flows}, Run: flow.Run, Env: env,
	})
	stop := start(t, w)
	defer stop()

	deadline := time.Now().Add(120 * time.Second)
	var j jobs.Job
	for {
		j, err = s.q.Get(ctx, s.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == jobs.Done || j.State == jobs.Failed || j.State == jobs.Skipped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job is still %s after 120 s", j.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if j.State != jobs.Done || (j.Outcome != "smaller" && j.Outcome != "bigger_kept") {
		t.Fatalf("job = state %s outcome %q reason %q; want done with smaller or bigger_kept", j.State, j.Outcome, j.Reason)
	}
	after, err := os.ReadFile(src)
	if err != nil || string(before) != string(after) {
		t.Fatalf("the clip changed or is gone: %v", err)
	}
	t.Logf("outcome %s, sizes %d -> %d", j.Outcome, derefSize(j.SizeBefore), derefSize(j.SizeAfter))
	if j.Outcome == "smaller" {
		out := filepath.Join(root, "holiday-shrunk.mkv")
		got, err := media.Probe(ctx, out)
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

func derefSize(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
