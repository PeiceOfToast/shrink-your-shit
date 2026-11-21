// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Slice 13's VFR case, on a real file: an mkv whose timestamps are stretched (so it has a variable frame rate)
// but whose header still says 30 fps and states a duration. ffprobe reports the nominal rate as the average, so
// duration × rate guesses more pictures than the file has; the probe cannot mark it VFR from its rates alone
// (they are close), and the encode's own report is what counts them right. With the report the job converts;
// on dev the duration × rate guess refuses it.
func TestRealVFRSourceIsCountedByItsReport(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "vfr.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=320x180:rate=30",
		"-vf", "setpts=1.5*PTS", "-fps_mode", "vfr", "-c:v", "libx264", "-crf", "24", "-an", src)
	info := probe(t, src)
	guess, exact := sourceFrameCount(info)
	if !exact || guess <= 0 {
		t.Fatalf("the sample no longer states a picture count (%d, exact %v): the test's subject is gone", guess, exact)
	}
	// What the decode really produced, from the same end-of-run report the encode uses.
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "level+verbose", "-nostats",
		"-i", src, "-map", "0:v:0", "-f", "null", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("decoding %s: %v\n%s", src, err, stderr.String())
	}
	k := info.MainVideoIndex()
	if k < 0 {
		t.Fatal("the sample has no video")
	}
	st, have := media.InputStreamStats(stderr.String(), 0, info.Streams[k].Index)
	if !have {
		t.Fatalf("no end-of-run report from decoding %s:\n%s", src, stderr.String())
	}
	if diff := math.Abs(float64(st.Frames-guess)) / float64(guess); diff <= 0.01 {
		t.Fatalf("the decode's %d frames are %.2f%% from the duration × rate %d: the sample is not VFR enough",
			st.Frames, diff*100, guess)
	}
	store, id := realJob(t, src, info.DurationSec)
	realWorker(store, media.OSExec{}).run(t)
	j, _ := store.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("status %s (%q), want done: the encode's report decoded %d frames, not the guess's %d",
			j.Status, j.Error, st.Frames, guess)
	}
	if text := logTextOf(store, id); !strings.Contains(text, "variable frame rate") {
		t.Errorf("the job log should say the original is variable frame rate:\n%s", text)
	}
}
