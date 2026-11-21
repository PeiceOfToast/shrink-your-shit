// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"slices"
	"strings"
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
	atEnd bool // the progress block reports the file's length: the finishing step
	ended bool // progress=end after the growth, as the muxer's last report

	mu    sync.Mutex
	size  int64    // the fake temp file's size, read by the watchdog's stat
	paths []string // every path the watchdog stat'd
}

func (x *growingExec) onDisk(path string) (int64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.paths = append(x.paths, path)
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
		out := "out_time_us=5000000"
		if x.atEnd {
			out = "out_time_us=100000000"
		}
		onLine(out)
		onLine("progress=continue")
	}
	if x.ended {
		onLine("progress=end")
	}
	if !x.hang {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for range 60 { // a hung encode: the clock runs on until the watchdog stops it (30 s of stall clock)
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
	return runGrowingWith(t, growRun{grow: grow, hang: hang})
}

// growRun is one fake encode for runGrowingWith: the growth, whether it hangs after it, whether its progress is at
// the file's length (the finishing step) and has ended, and FinishAfter (0: the default).
type growRun struct {
	grow        []int64
	hang        bool
	atEnd       bool
	ended       bool
	finishAfter time.Duration
}

func runGrowingWith(t *testing.T, r growRun) (*scenario, jobs.Job) {
	t.Helper()
	clock := &fakeClockAt{t: time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)}
	s := newScenario(t)
	x := &growingExec{FakeExec: s.exec, clock: clock, grow: r.grow, hang: r.hang, atEnd: r.atEnd, ended: r.ended}
	w := s.worker(Replace)
	w.stallClock = clock.now
	w.StallAfter = 2 * time.Second
	w.FinishAfter = r.finishAfter
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
	// the watchdog must stat the one staged temp file, the same path for the whole encode
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, p := range x.paths {
		if p != x.paths[0] || strings.HasSuffix(p, ".srt") {
			t.Fatalf("the watchdog stat'd %q, want one temp file (first %q)", p, x.paths[0])
		}
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

// ones is n growth steps of one byte: 24 is within the 28-byte bound for a 14-byte source, 28 reaches it.
func ones(n int) []int64 {
	g := make([]int64, n)
	for i := range g {
		g[i] = 1
	}
	return g
}

// In the finishing step (the position at the file's length) a growing file is alive, not stopped at StallAfter. This
// also holds before FinishAfter: the growth lasts 12 s of stall clock, well past the 2 s stall allowance.
func TestFinishingFileThatGrowsIsNotStopped(t *testing.T) {
	s, j := runGrowingWith(t, growRun{grow: ones(24), atEnd: true, finishAfter: time.Minute})
	if j.Status != jobs.Done {
		t.Fatalf("a finishing encode whose file grew was stopped: %s %q", j.Status, j.Error)
	}
	if n := logCount(t, s, "stopping it"); n != 0 {
		t.Errorf("%d stop lines for a finish that grew, want 0", n)
	}
}

// In the finishing step a file that stops growing is stopped at StallAfter, not at FinishAfter: the growth ends at
// 12 s, the encode hangs, and the stop comes two seconds of stall clock later. Before the change FinishAfter (a minute
// here) was the allowance, so this encode ran on.
func TestFinishingFileThatStopsGrowingIsStoppedAtStallAfter(t *testing.T) {
	s, j := runGrowingWith(t, growRun{grow: ones(24), hang: true, atEnd: true, finishAfter: time.Minute})
	if j.Status == jobs.Done {
		t.Fatalf("a hung finish whose file stopped growing finished: %q", j.Error)
	}
	if n := logCount(t, s, "no progress for 2s"); n == 0 {
		t.Errorf("no stall stop at StallAfter (status %s %q)", j.Status, j.Error)
	}
}

// FinishAfter bounds the finishing step even while its file grows: the position has not moved for 3 s of stall clock
// (the growth runs 14 s), so the finishing-step stop fires.
func TestFinishingFileGrowingPastFinishAfterHitsTheCeiling(t *testing.T) {
	s, j := runGrowingWith(t, growRun{grow: ones(28), atEnd: true, finishAfter: 3 * time.Second})
	if j.Status == jobs.Done {
		t.Fatalf("a finish that ran past FinishAfter finished: %q", j.Error)
	}
	if n := logCount(t, s, "the finishing step has run for more than 3s; stopping it"); n == 0 {
		t.Errorf("no finishing-step stop (status %s %q)", j.Status, j.Error)
	}
}

// After progress=end nothing is judged: the encode hangs for 30 s of stall clock, past both allowances, and no
// watchdog stop is written.
func TestEndedEncodeIsNotJudged(t *testing.T) {
	s, _ := runGrowingWith(t, growRun{hang: true, atEnd: true, ended: true, finishAfter: 3 * time.Second})
	if n := logCount(t, s, "stopping it"); n != 0 {
		t.Errorf("%d watchdog stop lines after progress=end, want 0", n)
	}
}
