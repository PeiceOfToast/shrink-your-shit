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

// finishExec reports the encode at the file's length (scenarioDuration, 100 s), or at 5 s of it with stuck: the
// finishing step (a +faststart rewrite) then prints nothing. reported is closed once that report has reached the
// worker; the test then moves the worker's clock, so the silence is measured rather than waited for. Closing
// release lets the encode finish as the fake makes it; with no release it waits for the stall stop.
type finishExec struct {
	*media.FakeExec
	stuck    bool
	reported chan struct{}
	release  chan struct{} // nil: the encode waits to be stopped
}

func (x finishExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	if x.stuck {
		onLine("frame=0")
		onLine("out_time_us=5000000")
	} else {
		onLine("frame=2500")
		onLine("out_time_us=100000000")
	}
	onLine("progress=continue")
	close(x.reported)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-x.release:
	}
	return x.FakeExec.Stream(ctx, onLine, name, args...)
}

// runClock runs w's one job in the background and returns x's report channel, so the test can move the clock
// once the encode has spoken.
func runClock(t *testing.T, w *Worker, x finishExec) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	select {
	case <-x.reported:
	case <-time.After(30 * time.Second):
		t.Fatal("the encode never reported")
	}
	return done
}

// Silence once the position is at the file's length gets the finishing allowance, not the stall one: a silent
// finish longer than StallAfter but within FinishAfter is not stopped.
func TestSilentFinishGetsItsOwnAllowance(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = 5 * time.Second
	x := finishExec{FakeExec: s.exec, reported: make(chan struct{}), release: make(chan struct{})}
	w.Exec = x
	done := runClock(t, w, x)
	clock.add(time.Second) // silent, but well inside FinishAfter
	close(x.release)
	waitDone(t, done)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("a silent finish within its allowance: %s %q", j.Status, j.Error)
	}
}

// Silence before the file's length keeps the stall allowance: an ffmpeg stuck at 5 s of 100 s is stopped at
// StallAfter, however large FinishAfter is.
func TestSilenceBeforeTheEndStillStops(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = 5 * time.Second
	x := finishExec{FakeExec: s.exec, stuck: true, reported: make(chan struct{})}
	w.Exec = x
	done := runClock(t, w, x)
	clock.add(300 * time.Millisecond) // the stall allowance, not the finishing one
	waitDone(t, done)
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") {
		t.Errorf("stuck before the end: %s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// The finishing allowance is bounded: a finish silent for longer than FinishAfter is stopped like any stall.
func TestFinishStopsAtItsOwnLimit(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.FinishAfter = time.Second
	x := finishExec{FakeExec: s.exec, reported: make(chan struct{})} // no release: the stop ends it
	w.Exec = x
	done := runClock(t, w, x)
	clock.add(3 * time.Second) // silent past FinishAfter
	waitDone(t, done)
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 1s") {
		t.Errorf("silent past FinishAfter: %s %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// reachedEnd's window is finishTolerance either side of the length (2 s): both edges are pinned, so narrowing the
// tolerance or dropping either side fails here.
func TestReachedEndEdges(t *testing.T) {
	cases := []struct {
		name string
		outS float64 // out_time, seconds
		durS float64 // the file's length, seconds (0: unknown)
		want bool
	}{
		{"at the length", 100, 100, true},
		{"2 s short, the edge", 98, 100, true},
		{"inside the short edge", 98.5, 100, true},
		{"just outside short", 97.9, 100, false},
		{"2 s past, the edge", 102, 100, true},
		{"just outside past", 102.1, 100, false},
		{"far past (a stuck encode)", 105, 100, false},
		{"far short (the middle)", 5, 100, false},
		{"length unknown", 100, 0, false},
		{"length unknown at 0", 0, 0, false},
	}
	for _, c := range cases {
		if got := reachedEnd(c.outS*1e6, c.durS); got != c.want {
			t.Errorf("%s: reachedEnd(%gs of %gs) = %v, want %v", c.name, c.outS, c.durS, got, c.want)
		}
	}
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
	// a stuck encode that reports a position well past a 2 s file's end is not finishing (it gets the stall allowance)
	past := NewRemoteJob("lease")
	past.Converting(5e6, now, 2)
	if past.finishing() {
		t.Error("a position well past the length is not the finish")
	}
}
