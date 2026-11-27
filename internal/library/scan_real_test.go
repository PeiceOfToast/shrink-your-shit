// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// requireRealScanTools is the media package's L-INST-7 rule for this package's one real test: skip
// only when a tool is missing and SYP_REQUIRE_FFMPEG is unset; CI sets it and also fails on any
// "--- SKIP: TestReal".
func requireRealScanTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s is not on PATH and SYP_REQUIRE_FFMPEG is set: the real scan test must not skip", tool)
			}
			t.Skipf("%s is not on PATH (set SYP_REQUIRE_FFMPEG=1 to fail instead)", tool)
		}
	}
}

// TestRealScanOverGeneratedClips runs one whole scan over a real H.264 clip and a text file with a
// video name, through the real prober. It is the only test here that talks to ffprobe.
func TestRealScanOverGeneratedClips(t *testing.T) {
	requireRealScanTools(t)
	st := newScanTest(t)
	st.scanner.Prober = media.MediaFuncs{ProbeFunc: media.Probe}

	cmd := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=30:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "clip.mp4")
	cmd.Dir = st.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	st.write("notes.mp4", "this is a text file with a video name")

	res := st.run()
	if res.Queued != 1 || res.SkippedBy[skipNotMedia] != 1 {
		t.Fatalf("result = %+v, want the clip queued and the text file skipped as not-media", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")

	f := st.row("clip.mp4")
	if f.State != Queued || f.Probe == nil {
		t.Fatalf("row = %+v, want queued with a probe", f)
	}
	if f.Probe.Container != "mov" {
		t.Errorf("container = %q, want mov", f.Probe.Container)
	}
	if len(f.Probe.Video) != 1 || f.Probe.Video[0].Codec != "h264" || f.Probe.Video[0].Width != 160 {
		t.Errorf("video = %+v, want one 160-wide h264 track", f.Probe.Video)
	}
	if f.Probe.DurationMS < 900 || f.Probe.DurationMS > 1100 {
		t.Errorf("duration = %d ms, want about 1000", f.Probe.DurationMS)
	}

	res = st.run()
	if res.Queued != 0 || res.New != 0 || res.Changed != 0 || res.Gone != 0 {
		t.Errorf("second scan = %+v, want the settled clip left alone", res)
	}
	if _, err := os.Stat(filepath.Join(st.dir, "clip.mp4")); err != nil {
		t.Errorf("the scan moved or removed the clip: %v", err)
	}
}
