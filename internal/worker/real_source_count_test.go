// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// countReadsExec runs real ffprobe and records which files were read just to count their video packets.
type countReadsExec struct {
	media.Exec
	mu      sync.Mutex
	counted []string
}

func (c *countReadsExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "ffprobe" {
		for _, a := range args {
			if a == "-count_packets" {
				c.mu.Lock()
				c.counted = append(c.counted, args[len(args)-1])
				c.mu.Unlock()
				break
			}
		}
	}
	return c.Exec.Output(ctx, name, args...)
}

// P2: a real file whose container states its picture count (ffmpeg's mkv writes a DURATION tag) is converted
// without reading the original a second time to count it, the check's number being the probe's own (see
// TestSourceCountIsNotReadAgainWhenTheContainerStatesIt for the fallback). Since P1 the new file's count comes
// from the playback run too, so on this sample nothing is counted at all; what this test keeps is that the
// original is never among the files counted.
func TestRealSourceCountIsNotReadAgain(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "4M", "-pix_fmt", "yuv420p", src)
	if n, exact := sourceFrameCount(probe(t, src)); !exact || n <= 0 {
		t.Fatalf("this sample's container no longer states its count (%d pictures, exact %v): the test's subject is gone", n, exact)
	}
	st, id := realJob(t, src, 6)
	rec := &countReadsExec{Exec: media.OSExec{}}
	w := realWorker(st, rec)
	w.Rule.CRF = 24
	w.run(t)
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	for _, f := range rec.counted {
		if f == src {
			t.Errorf("the original was read again to count its packets: %v", rec.counted)
		}
	}
	if len(rec.counted) > 1 {
		t.Errorf("files read to count their packets: %v; want at most the new file", rec.counted)
	}
}
