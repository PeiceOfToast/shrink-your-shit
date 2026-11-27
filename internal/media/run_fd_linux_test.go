// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package media

import (
	"context"
	"os"
	"runtime/debug"
	"testing"
)

// TestRunClosesItsPipes proves Run does not leak its two pipe read ends. The garbage collector is
// off for the duration, because an *os.File finalizer closes the pipe eventually and would hide the
// leak; a burst of short jobs between collections would otherwise reach the fd limit and fail with
// EMFILE far from the cause. Review 801 (N1): the pipe rewrite left these open on the normal path.
func TestRunClosesItsPipes(t *testing.T) {
	before, err := openFDs()
	if err != nil {
		t.Fatalf("count fds: %v", err)
	}
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)

	const runs = 100
	for i := 0; i < runs; i++ {
		res, err := Run(context.Background(), []string{"true"}, nil)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("run %d = exit %d, %v; want exit 0", i, res.ExitCode, err)
		}
	}
	after, err := openFDs()
	if err != nil {
		t.Fatalf("count fds: %v", err)
	}
	// Two fds per run would be 200 here; a little slack covers the descriptors other goroutines
	// hold while the count is taken.
	if after > before+8 {
		t.Fatalf("open fds grew from %d to %d over %d runs; Run is leaking its pipe read ends", before, after, runs)
	}
}

func openFDs() (int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
