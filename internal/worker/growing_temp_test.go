// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// growStep is how far the fake clock moves for each step of the fake encode.
const growStep = 500 * time.Millisecond

// growingExec is an encode whose progress block is frozen (out_time, frame and total_size repeat) while its temp
// file grows on disk, as CT 124's job 93 did. Each step adds grow[i] bytes to the fake file. Once the growth ends it
// finishes, or with hang it keeps the clock running until the watchdog stops it.
type growingExec struct {
	*media.FakeExec
	clock *fakeClockAt
	grow  []int64
	hang  bool

	mu   sync.Mutex
	size int64 // the fake temp file's size, read by the watchdog's stat
}

func (x *growingExec) onDisk(string) (int64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.size, nil
}

func (x *growingExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for _, n := range x.grow {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		x.mu.Lock()
		x.size += n
		x.mu.Unlock()
		x.clock.add(growStep)
		onLine("frame=100")
		onLine("total_size=10")
		onLine("out_time_us=5000000")
		onLine("progress=continue")
	}
	if !x.hang {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for range 400 { // a hung encode: the clock runs on until the watchdog stops it
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		x.clock.add(growStep)
	}
	return "", context.DeadlineExceeded
}

// runGrowing runs one job through the worker on the stall clock, with the growth in grow, and returns the scenario
// and the job when it has ended. The source is 14 bytes, so the output may reach 28 bytes (twice the source).
func runGrowing(t *testing.T, grow []int64, hang bool) (*scenario, jobs.Job) {
	t.Helper()
	clock := &fakeClockAt{t: time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)}
	s := newScenario(t)
	x := &growingExec{FakeExec: s.exec, clock: clock, grow: grow, hang: hang}
	w := s.worker(Replace)
	w.stallClock = clock.now
	w.StallAfter = 2 * time.Second
	w.FrozenPositionFactor = 1000
	w.sizeOnDisk = x.onDisk
	w.Exec = x
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	// the stall decisions run on the fake clock; this real-time bound only has to outlast a loaded host's disk
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the job did not end")
	}
	return s, s.job()
}

// A frozen progress block with a temp file still growing within twice the source is an encode that is alive: it
// finishes, and no stall stop is written.
func TestGrowingTempFileKeepsAFrozenEncodeAlive(t *testing.T) {
	grow := make([]int64, 24) // 24 bytes in all: within the 28-byte bound
	for i := range grow {
		grow[i] = 1
	}
	s, j := runGrowing(t, grow, false)
	if j.Status != jobs.Done {
		t.Fatalf("status %s %q: a frozen progress block with a growing temp file was stopped", j.Status, j.Error)
	}
	if n := logCount(t, s, "no progress"); n != 0 {
		t.Errorf("%d stall stop lines for a growing file, want 0", n)
	}
}

// The growth ends and the encode hangs: the watchdog still stops it, two seconds of stall clock after the last
// growth, with the stall message.
func TestGrowingTempFileThatStopsIsStopped(t *testing.T) {
	grow := make([]int64, 24)
	for i := range grow {
		grow[i] = 1
	}
	s, j := runGrowing(t, grow, true)
	if j.Status == jobs.Done {
		t.Fatalf("a hung encode whose temp file stopped growing finished: %q", j.Error)
	}
	if n := logCount(t, s, "no progress for 2s"); n == 0 {
		t.Errorf("no stall stop after the growth ended (status %s %q)", j.Status, j.Error)
	}
}

// Growth past twice the source is not progress: the watchdog stops the encode and says the output is too large.
func TestGrowthPastTwiceTheSourceIsStopped(t *testing.T) {
	s, j := runGrowing(t, []int64{1000, 1000, 1000}, true)
	if j.Status == jobs.Done {
		t.Fatalf("an output of 3000 bytes from a 14-byte source finished: %q", j.Error)
	}
	if n := logCount(t, s, "more than twice the original's size"); n == 0 {
		t.Errorf("no over-size stop (status %s %q)", j.Status, j.Error)
	}
}
