// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// outFrozenExec keeps out_time on one position for every report (a subtitle gap: out_time follows the
// most-behind stream and freezes while the encode goes on). frames says whether frame= grows with each report;
// sizeStep is how far total_size= grows per report (0: no total_size=). Each report takes a little real time (for
// the stall stop) and advances the fake clock by tick (for the 30 s warning). With ticks 0 it reports until the
// stall stop cancels it.
type outFrozenExec struct {
	*media.FakeExec
	clock    *fakeClockAt
	tick     time.Duration
	ticks    int
	frames   bool
	sizeStep int64
}

func (x *outFrozenExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for i := 0; x.ticks == 0 || i < x.ticks; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
		if x.frames {
			onLine(fmt.Sprintf("frame=%d", i))
		}
		if x.sizeStep > 0 {
			onLine(fmt.Sprintf("total_size=%d", x.sizeStep*int64(i+1)))
		}
		onLine("out_time_us=5000000")
		onLine("progress=continue")
		x.clock.add(x.tick)
	}
	return x.FakeExec.Stream(ctx, onLine, name, args...) // then the new file, as the fake makes it
}

// encodeLines returns the job's encode-section log lines, the warnings and the stall stops among them.
func encodeLines(t *testing.T, s *scenario) (warn int, stopped bool) {
	t.Helper()
	lines, _ := s.store.LogLines(s.id)
	for _, l := range lines {
		if l.Section != "encode" {
			continue
		}
		if l.Level == jobs.LevelWarning && strings.Contains(l.Text, "has not moved for 30s") {
			warn++
		}
		if strings.Contains(l.Text, "no progress for") {
			stopped = true
		}
	}
	return warn, stopped
}

// Bug 2: out_time frozen for longer than StallAfter, with frame= (or total_size=) still growing within twice the
// source's size, is an encode that is alive: not stopped, and no "has not moved" warning, though the fake clock runs
// past 30 s without out_time moving. The scenario's source is 14 bytes, so the bytes grow 1 a report (20 reports, 20 bytes).
func TestFrozenOutTimeIsAliveWhileFramesOrBytesGrow(t *testing.T) {
	for _, c := range []struct {
		name     string
		frames   bool
		sizeStep int64
	}{{"frames grow", true, 0}, {"bytes grow", false, 1}, {"both grow", true, 1}} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
			w := s.worker(Replace)
			w.Now = clock.now
			w.StallAfter = 300 * time.Millisecond // real time: the encode below runs ~600 ms, twice this
			w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second, ticks: 20, frames: c.frames, sizeStep: c.sizeStep}
			if _, err := w.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if j := s.job(); j.Status != jobs.Done {
				t.Fatalf("status %s %q, want done: the encode was alive", j.Status, j.Error)
			}
			if warn, stopped := encodeLines(t, s); warn != 0 || stopped {
				t.Fatalf("%d 'has not moved' warnings, stopped %v; want none", warn, stopped)
			}
		})
	}
}

// Bug 2, the control: out_time, frame= and total_size all frozen is still stopped at StallAfter, and the warning
// is written (the fake clock runs past 30 s).
func TestFullyFrozenEncodeIsStillStopped(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") {
		t.Fatalf("status %s %q, want failed with no progress for 300ms", j.Status, j.Error)
	}
	if warn, _ := encodeLines(t, s); warn != 1 {
		t.Fatalf("%d 'has not moved' warnings, want 1", warn)
	}
	s.assertOriginalSafe()
}

// Bug 2, the bound: frames still growing with out_time frozen is stopped once total_size passes twice the source's
// size (a runaway output is not progress); the stall stop applies, and the warning is written.
func TestFrozenOutTimeStopsPastTheOutputBound(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = 300 * time.Millisecond
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: 10 * time.Second, frames: true, sizeStep: 1000}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no progress for 300ms") {
		t.Fatalf("status %s %q, want failed with no progress for 300ms", j.Status, j.Error)
	}
	if warn, _ := encodeLines(t, s); warn != 1 {
		t.Fatalf("%d 'has not moved' warnings, want 1", warn)
	}
	s.assertOriginalSafe()
}
