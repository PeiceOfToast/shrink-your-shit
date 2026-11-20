// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// finishExec reports the encode at the file's length (scenarioDuration, 100 s), then prints nothing for silent:
// the finishing step (a +faststart rewrite) has no progress line. It ends when the stall stop cancels it.
type finishExec struct {
	*media.FakeExec
	silent time.Duration
}

func (x finishExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	onLine("frame=2500")
	onLine("out_time_us=100000000")
	onLine("progress=continue")
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(x.silent):
	}
	return x.FakeExec.Stream(ctx, onLine, name, args...)
}

// runOnce runs w's one job to its end and waits for it.
func runOnce(t *testing.T, w *Worker) {
	t.Helper()
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
}

// Silence once the position is at the file's length gets the finishing allowance, not the stall one: a silent
// finish longer than StallAfter but within FinishAfter is not stopped.
func TestSilentFinishGetsItsOwnAllowance(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = 5 * time.Second
	w.Exec = finishExec{FakeExec: s.exec, silent: time.Second}
	runOnce(t, w)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("a silent finish within its allowance: %s %q", j.Status, j.Error)
	}
}

// Silence before the file's length keeps the stall allowance: an ffmpeg stuck at 5 s of 100 s is stopped at
// StallAfter, however large FinishAfter is.
func TestSilenceBeforeTheEndStillStops(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = 5 * time.Second
	w.Exec = positionExec{FakeExec: s.exec} // step 0: stuck at 5 s
	runOnce(t, w)
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") {
		t.Errorf("stuck before the end: %s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// The finishing allowance is bounded: a finish silent for longer than FinishAfter is stopped like any stall.
func TestFinishStopsAtItsOwnLimit(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = time.Second
	w.Exec = finishExec{FakeExec: s.exec, silent: 3 * time.Second}
	runOnce(t, w)
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 1s") {
		t.Errorf("silent past FinishAfter: %s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// The remote worker's position counts as the finish only at the file's length, and only when the length is known.
func TestRemotePositionAtTheLengthIsFinishing(t *testing.T) {
	now := time.Now()
	rj := NewRemoteJob("lease")
	rj.Converting(5e6, now, 100)
	if rj.finishing() {
		t.Error("5 s of 100 s is not the finish")
	}
	rj.Converting(100e6, now, 100)
	if !rj.finishing() {
		t.Error("the file's length is the finish")
	}
	unknown := NewRemoteJob("lease")
	unknown.Converting(100e6, now, 0)
	if unknown.finishing() {
		t.Error("an unknown length is never the finish")
	}
}
