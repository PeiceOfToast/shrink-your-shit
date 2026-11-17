// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A good re-encode with sparse keyframes (one every 10 s) must pass the picture check at a sample just
// after a keyframe. The seek lands ~10 s before the sample's start, and the read window must still cover
// the whole sample: it used to be counted from the landing, so the sample's end was never read and a good
// file was refused (CT 124: 0.869 at 0:19:58). Measured on the old code: 0.872 here.
func TestRealSSIMSampleWithSparseKeyframesPasses(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=60", "-c:v", "libx264", "-g", "24", "-crf", "18",
		"-pix_fmt", "yuv420p", src)
	out := filepath.Join(dir, "b.mkv")
	runFFmpeg(t, "-i", src, "-c:v", "libx264", "-crf", "23", "-preset", "ultrafast", "-g", "240", "-keyint_min", "240",
		"-sc_threshold", "0", "-pix_fmt", "yuv420p", out)

	w := realWorker(nil, media.OSExec{})
	mid := segment{start: 31.9, length: 10} // from 19.9 s, the seek lands at the 10 s keyframe
	score, _, err := w.ssimSegment(context.Background(), src, out, 0, 0, mid, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if score < DefaultMinSSIM {
		t.Fatalf("a good re-encode scored %.3f at the middle sample (needs %.2f): the read window missed the sample", score, DefaultMinSSIM)
	}
}
