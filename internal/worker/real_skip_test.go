// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Slice 13, review-588 round 3, on a real file: a steady-rate .ts from which a bitstream filter silently
// dropped packets. The demuxer never reads them, so the encode reads exactly what it decodes — short of
// duration × rate — prints no error, and the output keeps the original's length; every count looks fine, yet
// frames are gone. What says so is the spacing of the frames that remain: they still sit at the 25 fps
// interval, with gaps where the skipped ones were. The original is kept, not replaced.
func TestRealSilentlySkippedStreamIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	base, skip := filepath.Join(dir, "base.ts"), filepath.Join(dir, "skip.ts")
	// All-intra h264 in mpegts: the worker's rules convert mpegts whose codec is not h264 by skipping it.
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20",
		"-c:v", "libx264", "-b:v", "2M", "-g", "1", "-x264-params", "keyint=1:min-keyint=1", "-f", "mpegts", base)
	runFFmpeg(t, "-i", base, "-c", "copy", "-bsf:v", "noise=dropamount=25", "-f", "mpegts", skip)

	info := probe(t, skip)
	guess, exact := sourceFrameCount(info)
	if !exact || guess < 400 {
		t.Fatalf("the sample no longer states a picture count (%d, exact %v): the test's subject is gone", guess, exact)
	}
	follows, err := media.FramesFollowNominalRate(context.Background(), media.OSExec{}, "ffprobe", skip,
		info.MainVideoOrdinal(), nominalFrameRate(info))
	if err != nil || !follows {
		t.Fatalf("the sample's frames do not follow its nominal rate (%v, %v): the test's subject is gone", follows, err)
	}
	// What the demuxer really handed the decoder, from the same end-of-run report the encode uses.
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "level+verbose", "-nostats",
		"-i", skip, "-map", "0:v:0", "-f", "null", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("decoding %s: %v\n%s", skip, err, stderr.String())
	}
	st, have := media.InputStreamStats(stderr.String(), 0, info.Streams[info.MainVideoIndex()].Index)
	if !have {
		t.Fatalf("no end-of-run report from decoding %s:\n%s", skip, stderr.String())
	}
	if st.Packets != st.Frames {
		t.Fatalf("the sample reads %d packets and decodes %d: that is a decode loss, not a silent skip", st.Packets, st.Frames)
	}
	if st.Frames >= guess || float64(guess-st.Frames) <= 0.01*float64(guess) {
		t.Fatalf("the sample's %d frames are not well short of duration × rate %d: the test's subject is gone", st.Frames, guess)
	}

	store, id := realJob(t, skip, info.DurationSec)
	realWorker(store, media.OSExec{}).run(t)
	j, _ := store.Get(id)
	if j.Status != jobs.Kept {
		t.Fatalf("status %s (%q), want kept: the stream's %d frames were skipped, and its frames keep the rate",
			j.Status, j.Error, guess-st.Frames)
	}
	if text := logTextOf(store, id); !strings.Contains(text, "frames were not decoded") {
		t.Errorf("the job log should name the frames that were not decoded:\n%s", text)
	}
}
