// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The starts of a long file's playback samples: a minute each, from 0 every 10 minutes, the last one 65 s
// before the end. The unchecked stretch is the time between one sample's end and the next one's start.
func TestPlaybackStartsCoverALongFile(t *testing.T) {
	cases := []struct {
		duration float64
		count    int
	}{
		{3*3600 + 60, 19}, // just over the 3-hour limit
		{4 * 3600, 25},
		{10 * 3600, 61},
	}
	for _, c := range cases {
		checkPlaybackStarts(t, c.duration, c.count)
	}
	// every whole second from just over 3 h to 10 h: the gap before the last sample can be any length up to 599 s
	for d := 3*3600 + 60.0; d <= 10*3600; d++ {
		checkPlaybackStarts(t, d, -1)
	}
}

// checkPlaybackStarts checks one duration's samples: the count when count >= 0, the first at 0, the last
// near the end, sorted with no overlaps, and no unplayed stretch longer than playbackEvery-60 (540 s), the
// one before the last sample included.
func checkPlaybackStarts(t *testing.T, duration float64, count int) {
	t.Helper()
	starts := playbackStarts(duration)
	if count >= 0 && len(starts) != count {
		t.Errorf("duration %.0f s: %d samples, want %d", duration, len(starts), count)
	}
	if starts[0] != 0 {
		t.Errorf("duration %.0f s: first sample starts at %.0f, want 0", duration, starts[0])
	}
	last := starts[len(starts)-1]
	if last+60 > duration || last+60 < duration-10 {
		t.Errorf("duration %.0f s: last sample ends at %.0f, want near the end", duration, last+60)
	}
	for i := 1; i < len(starts); i++ {
		prevEnd := starts[i-1] + 60
		switch {
		case starts[i] <= starts[i-1]:
			t.Errorf("duration %.0f s: samples %d and %d are not in order or repeat (%.0f, %.0f)", duration, i-1, i, starts[i-1], starts[i])
		case starts[i] < prevEnd:
			t.Errorf("duration %.0f s: sample %d starts at %.0f, before sample %d ends at %.0f", duration, i, starts[i], i-1, prevEnd)
		case starts[i]-prevEnd > playbackEvery-60:
			t.Errorf("duration %.0f s: %.0f s unchecked between samples %d and %d", duration, starts[i]-prevEnd, i-1, i)
		}
	}
}

// A real-ffmpeg check of the same file: a 300 s Matroska file with bytes damaged at 105-115 s, the middle third.
// Today's three samples (0, 120 and 235 s) miss that stretch and pass the file; the sampled check finds it. The
// uncorrupted file passes. (MKV, not TS: the local ffmpeg segfaults on TS, the test machine's does not.)
func TestRealLongFilePlaybackFindsDamageTheThreeSamplesMissed(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "long.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=300:size=320x180:rate=25", "-c:v", "libx264", "-b:v", "300k",
		"-pix_fmt", "yuv420p", "-g", "25", good)
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	damaged := append([]byte(nil), data...)
	for i := len(data) * 105 / 300; i < len(data)*115/300; i++ {
		damaged[i] ^= 0xA5
	}
	bad := filepath.Join(dir, "damaged.mkv")
	if err := os.WriteFile(bad, damaged, 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{ID: "real", Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", FullDecodeMaxSec: 10} // 300 s is long here
	oldEvery := playbackEvery
	playbackEvery = 30 // samples every 30 s: the stretch at 105-115 s is inside a sample
	t.Cleanup(func() { playbackEvery = oldEvery })
	ctx := context.Background()

	goodInfo := probe(t, good)
	if _, _, err := w.decodeCheck(ctx, goodInfo, good); err != nil {
		t.Fatalf("the uncorrupted long file was refused: %v", err)
	}
	badInfo := probe(t, bad)
	if _, _, err := w.decodeCheck(ctx, badInfo, bad); err == nil {
		t.Fatal("the sampled playback check passed a file damaged at 105-115 s")
	}
	// Control: the three samples the check used before this change pass the same damaged file.
	var fps float64
	if v := badInfo.Video(); v != nil {
		fps = v.FrameRate
	}
	for _, start := range []float64{0, 300/2 - 30, 300 - 65} {
		if _, _, err := w.decodeRun(ctx, bad, badInfo, start, 60, 60, fps); err != nil {
			t.Fatalf("the old three samples fail at %.0f s, so the control proves nothing: %v", start, err)
		}
	}
}
