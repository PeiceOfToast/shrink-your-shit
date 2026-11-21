// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// The scanner's probe of a file fills the probe cache; the worker's source probe of the same file version is
// then read from it, and a changed file is probed again.
func TestProbeSourceReusesTheScannerCache(t *testing.T) {
	const probeJSON = `{"streams":[{"codec_type":"video","codec_name":"h264","bit_rate":"8000000"}],"format":{"duration":"60","size":"5"}}`
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(t.TempDir(), "a.mkv")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	x := &media.FakeExec{Out: []byte(probeJSON)}
	ctx := context.Background()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Probe(ctx, st, x, "ffprobe", path, fi); err != nil { // the scan
		t.Fatal(err)
	}
	w := &Worker{Queue: st, Exec: x, FFprobe: "ffprobe"}
	if _, err := w.probeSource(ctx, path); err != nil { // the job
		t.Fatal(err)
	}
	if n := ffprobeCalls(x, path); n != 1 {
		t.Errorf("scan then job on the same file version: ffprobe ran %d times, want 1", n)
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := w.probeSource(ctx, path); err != nil {
		t.Fatal(err)
	}
	if n := ffprobeCalls(x, path); n != 2 {
		t.Errorf("changed mtime: ffprobe ran %d times, want 2", n)
	}
}

// ffprobeCalls counts the ffprobe runs that read path (its last argument).
func ffprobeCalls(x *media.FakeExec, path string) int {
	n := 0
	for _, c := range x.Recorded() {
		if c[0] == "ffprobe" && c[len(c)-1] == path {
			n++
		}
	}
	return n
}

// probeReadsExec runs real ffprobe and records each metadata probe of a path, so a job can be checked for its
// source probes (its lead reads are not probes and are not recorded).
type probeReadsExec struct {
	media.Exec
	mu     sync.Mutex
	probed []string
}

func (p *probeReadsExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	probe := false
	for _, a := range args {
		if a == "-show_format" { // media.Probe, the metadata probe; a lead read asks for packets or frames
			probe = true
		}
	}
	if name == "ffprobe" && probe {
		p.mu.Lock()
		p.probed = append(p.probed, args[len(args)-1])
		p.mu.Unlock()
	}
	return p.Exec.Output(ctx, name, args...)
}

// A real job probes its source once: the worker reads the probe cache first, and the first probe fills it.
// A job may read the source again for its lead (the packets and first frames of its first seconds); that read is
// not a probe, and the recorder counts the metadata probes only.
func TestRealSourceIsProbedOncePerJob(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=6:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "4M", "-pix_fmt", "yuv420p", src)
	st, id := realJob(t, src, 6)
	rec := &probeReadsExec{Exec: media.OSExec{}}
	w := realWorker(st, rec)
	w.Rule.CRF = 24
	w.run(t)
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	n := 0
	for _, f := range rec.probed {
		if f == src {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the source was probed %d times in one job, want 1: %v", n, rec.probed)
	}
}
