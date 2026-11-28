// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// withReplaceSave is the flow with its save step set to replace the original. Publish refuses replace until the
// un-grey is merged, so the revision loader hands the worker this flow; the worker still runs the real flow.
func withReplaceSave(t *testing.T, f flow.Flow) flow.Flow {
	t.Helper()
	for i, ref := range f.Steps {
		if ref.Type != "file.save" {
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(ref.Settings, &s); err != nil {
			t.Fatal(err)
		}
		s["where"] = "replace"
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		f.Steps[i].Settings = b
	}
	return f
}

// waitSettled waits until the job is done, failed or skipped, and returns it.
func waitSettled(t *testing.T, s storedJob) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		j, err := s.q.Get(s.ctx, s.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == jobs.Done || j.State == jobs.Failed || j.State == jobs.Skipped {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job is still %s after 120 s", j.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRealRunAgainConvertsTheCurrentFileAndReplacesSafely: a done job runs again on its row and finishes. The second
// run replaces the file at its path (the original), so the original is in the trash, byte for byte, and the file at the
// name is the new HEVC file (S3-6b: the real test and the replace-safe test).
func TestRealRunAgainConvertsTheCurrentFileAndReplacesSafely(t *testing.T) {
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

	var replaceNow atomic.Bool
	env := flow.Env{
		Media:  media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
		Safety: flow.RealSafety{}, Pick: flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) {
			rev, err := s.flows.Revision(ctx, id, r)
			if err != nil {
				return flow.Flow{}, err
			}
			if replaceNow.Load() {
				return withReplaceSave(t, rev.Flow), nil
			}
			return rev.Flow, nil
		},
		Now: time.Now, Sleep: time.Sleep, AppVersion: "test",
	}
	env.Replacer = flow.RealReplacer{Journal: safety.NewJournal(s.db)}
	w := New(Config{
		Slots: 1, WorkerID: "local", DataDir: t.TempDir(), Tick: 5 * time.Millisecond,
		Source: Builtin{Queue: s.q, DB: s.db, Flows: s.flows}, Run: flow.Run, Env: env,
	})
	stop := start(t, w)
	defer stop()

	first := waitSettled(t, s)
	if first.State != jobs.Done || first.Attempt != 1 {
		t.Fatalf("first run = state %s attempt %d, want done on attempt 1", first.State, first.Attempt)
	}

	// Run again on the done job: the same row goes back to waiting and the worker finishes it, now as a replace.
	replaceNow.Store(true)
	if err := s.q.RunAgain(ctx, s.jobID); err != nil {
		t.Fatalf("run again: %v", err)
	}
	second := waitSettled(t, s)
	if second.ID != s.jobID || second.Attempt != 2 {
		t.Fatalf("second run = job %d attempt %d, want the same row on attempt 2", second.ID, second.Attempt)
	}
	if second.State != jobs.Done || (second.Outcome != "smaller" && second.Outcome != "bigger_kept") {
		t.Fatalf("second run = state %s outcome %q reason %q; want done", second.State, second.Outcome, second.Reason)
	}

	// Replace-safe: the original is in the trash under the job's name, byte for byte, never touched by the run.
	trash := filepath.Join(root, ".syp-trash", fmt.Sprintf("holiday.%d.mkv", s.jobID))
	kept, err := os.ReadFile(trash)
	if err != nil {
		t.Fatalf("the original is not in the trash: %v", err)
	}
	if !bytes.Equal(kept, before) {
		t.Errorf("the trash copy differs from the original")
	}
	if second.Outcome == "smaller" {
		got, err := media.Probe(ctx, src)
		if err != nil {
			t.Fatalf("probe the file at the name: %v", err)
		}
		if len(got.Video) != 1 || got.Video[0].Codec != "hevc" {
			t.Errorf("the file at the name = %+v, want the new HEVC file", got.Video)
		}
	}
}
