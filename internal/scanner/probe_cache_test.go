// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// ffprobeRuns counts the ffprobe calls a fake exec has seen.
func ffprobeRuns(f *media.FakeExec) int {
	n := 0
	for _, c := range f.Recorded() {
		if c[0] == "ffprobe" {
			n++
		}
	}
	return n
}

// One ffprobe per file version: the same size and modification time is read from the cache, a changed
// modification time or size is probed again.
func TestProbeReusesTheCacheForTheSameFileVersion(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(t.TempDir(), "a.mkv")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &media.FakeExec{Out: []byte(h264Big)}
	ctx := context.Background()

	stat := func() os.FileInfo {
		t.Helper()
		fi, err := os.Stat(path) // a fresh stat each time, as the worker takes it
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}

	if _, err := Probe(ctx, st, f, "ffprobe", path, stat()); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(ctx, st, f, "ffprobe", path, stat()); err != nil {
		t.Fatal(err)
	}
	if n := ffprobeRuns(f); n != 1 {
		t.Errorf("same size and mtime: ffprobe ran %d times, want 1", n)
	}

	// A changed modification time is a new file version: probed again.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(ctx, st, f, "ffprobe", path, stat()); err != nil {
		t.Fatal(err)
	}
	if n := ffprobeRuns(f); n != 2 {
		t.Errorf("changed mtime: ffprobe ran %d times, want 2", n)
	}

	// A changed size is a new file version too.
	if err := os.WriteFile(path, []byte("1234567890"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later.Add(time.Minute), later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(ctx, st, f, "ffprobe", path, stat()); err != nil {
		t.Fatal(err)
	}
	if n := ffprobeRuns(f); n != 3 {
		t.Errorf("changed size: ffprobe ran %d times, want 3", n)
	}
}
