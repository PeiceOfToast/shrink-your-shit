// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// L2: a real encode's summary counters say what it took: the ffmpeg runs it needed, and the bytes it wrote
// (the new file's). The checks (playback, picture comparison) are runs too, so there are more than one.
func TestRealRunCounters(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "4M", "-pix_fmt", "yuv420p", src)
	st, id := realJob(t, src, 6)
	w := realWorker(st, media.OSExec{})
	w.Rule.CRF = 24
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	passes, written, ok, err := st.JobStats(id)
	if err != nil || !ok {
		t.Fatalf("the counters were not recorded (ok=%v): %v", ok, err)
	}
	if passes < 2 { // the encode, and at least the playback test
		t.Errorf("passes %d: the checks are not counted", passes)
	}
	if written != j.SizeAfter {
		t.Errorf("bytesWritten %d, but the new file is %d bytes", written, j.SizeAfter)
	}
}
