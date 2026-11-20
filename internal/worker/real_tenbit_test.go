// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A 10-bit SDR source is written 10-bit, end to end: the plan carries the source's depth, the new picture is
// yuv420p10le at 10 bits, and the output check accepts it (an 8-bit encode would be refused and the original kept).
func TestRealTenBitSDRStaysTenBit(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	runFFmpeg(t,
		"-f", "lavfi", "-i", "testsrc2=duration=8:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=duration=8",
		"-c:v", "libx264", "-pix_fmt", "yuv420p10le", "-b:v", "1M", "-c:a", "aac", src)
	if v := probe(t, src).Video(); v == nil || v.BitDepth != 10 {
		t.Fatalf("the sample is not 10-bit: %+v", v)
	}
	st, id := realJob(t, src, 8)
	if err := realWorker(st, media.OSExec{}).run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("status %s: %s", j.Status, j.Error)
	}
	v := probe(t, src).Video()
	if v == nil || v.Codec != "hevc" || v.BitDepth != 10 || v.PixFmt != "yuv420p10le" {
		t.Errorf("the new picture %+v, want 10-bit HEVC (yuv420p10le) kept", v)
	}
	if tempLeft(src) {
		t.Error("temp file left")
	}
}
