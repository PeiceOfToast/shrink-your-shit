// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// makeResultClip writes a real H.264 MKV with sound for the result-check tests:
// testsrc2 at a modest bitrate, so a high-bitrate HEVC encode of it is bigger.
func makeResultClip(t *testing.T, path string, seconds int) {
	t.Helper()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=" + strconv.Itoa(seconds),
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + strconv.Itoa(seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "2M", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
}

// realResultRun is Run with the real tools and the real safety chain, the way a worker runs it.
func realResultRun(t *testing.T, root, src string, rev flow.Flow) flow.Result {
	t.Helper()
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
		Media:        media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
		Safety:       flow.RealSafety{},
		Pick:         flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) { return rev, nil },
		Now:          time.Now,
		Sleep:        time.Sleep,
		AppVersion:   "test",
	}
	return flow.Run(context.Background(), in, env)
}

// onlyFileIs fails when a run left anything beside the file it was given.
func onlyFileIs(t *testing.T, root, name string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != name {
			t.Errorf("the run left %q beside the original", e.Name())
		}
	}
}

// TestRealBiggerResultKeepsTheOriginal is L-SAFE-7 on real files: an encode at a bitrate far above
// the source's makes a bigger new file, the size rule keeps the original, and the result is
// bigger_kept with both sizes, not a failure (L-SAFE-7).
func TestRealBiggerResultKeepsTheOriginal(t *testing.T) {
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
	src := filepath.Join(root, "bigger.mkv")
	makeResultClip(t, src, 4)
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	f := starterFlow(t)
	// CBR, not VBR: libx265 undershoots a VBR target badly on simple pictures, and this test needs
	// the size rule reached on a real file every time.
	f.Steps[0].Settings = json.RawMessage(`{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu",` +
		`"rateControl":"cbr","bitrateLevel":"custom","avgMbps":40}`)
	res := realResultRun(t, root, src, f)

	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != "size.not_smaller" {
		t.Fatalf("result = %+v; want bigger_kept size.not_smaller", res)
	}
	if res.SizeAfter <= res.SizeBefore {
		t.Errorf("sizes = %d -> %d; the test needs a bigger result", res.SizeBefore, res.SizeAfter)
	}
	if res.SizeBefore == 0 || res.SizeAfter == 0 {
		t.Errorf("sizes = %d -> %d; both sizes must be reported", res.SizeBefore, res.SizeAfter)
	}
	if res.OutputPath != "" {
		t.Errorf("output = %q; a bigger result is not placed", res.OutputPath)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("the original is gone: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the original changed: %d -> %d bytes", len(before), len(after))
	}
	onlyFileIs(t, root, "bigger.mkv")
}

// TestRealDamagedOriginalIsKeptNotConverted is L-SAFE-4 end to end: a truncated original is kept
// with its reason and nothing is encoded (the encoder is never to blame).
func TestRealDamagedOriginalIsKeptNotConverted(t *testing.T) {
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
	full := filepath.Join(root, "full.mkv")
	makeResultClip(t, full, 4)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "damaged.mkv")
	if err := os.WriteFile(src, data[:len(data)*6/10], 0o644); err != nil {
		t.Fatal(err)
	}

	res := realResultRun(t, root, src, starterFlow(t))
	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != safety.CodeOriginalDamaged {
		t.Fatalf("result = %+v; want bigger_kept %s", res, safety.CodeOriginalDamaged)
	}
	if res.Message == "" {
		t.Error("no sentence said why the original was kept")
	}
	onlyFileIs(t, root, "damaged.mkv")
}
