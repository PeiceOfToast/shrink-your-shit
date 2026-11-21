// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A remote encode whose out_time is frozen is stopped at the frozen-position ceiling while its frames keep growing.
// Frames move the stall clock, not the ceiling's, so the ceiling is the only check that can stop it here (P3a).
func TestRemoteFrozenPositionCeilingStopsAliveRemoteEncode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Worker{ID: "t", StallAfter: 40 * time.Millisecond, FrozenPositionFactor: 2}
	w.current = 7
	w.cancelJob = cancel
	rj := NewRemoteJob("lease")
	rj.ConvertingFrames(1e6, 0, 0, 14, time.Now(), 100)
	stopWatch := w.watchRemoteStall(7, rj)
	defer stopWatch()
	go func() { // frames grow every 2 ms, out_time stays at 1 s; the output stays within twice the 14-byte source
		for i := int64(1); ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Millisecond):
			}
			rj.ConvertingFrames(1e6, i, 0, 14, time.Now(), 100)
		}
	}()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a remote encode with a frozen out_time, frames still growing, was not stopped by the ceiling")
	}
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped != stopStall {
		t.Errorf("stopped for %v, want stopStall", stopped)
	}
	if got := time.Duration(w.stalledFor.Load()); got != 80*time.Millisecond {
		t.Errorf("stopped at %s, want the ceiling 80ms (2 x StallAfter)", got)
	}
}

// The default ceiling is 3 x StallAfter, 30 min on the default 10 min StallAfter. Pinned, so 3 -> 2 goes red (P3b).
func TestDefaultFrozenPositionFactorIsThree(t *testing.T) {
	w := &Worker{}
	if got := w.frozenFactor(); got != 3 {
		t.Fatalf("default frozen-position factor %d, want 3", got)
	}
	if got := w.frozenFactor() * DefaultStallAfter; got != 30*time.Minute {
		t.Fatalf("default frozen-position ceiling %s, want 30m0s", got)
	}
}

// The local ceiling's stop line names the position, not "no progress": frames were still arriving (P3c).
func TestFrozenPositionCeilingSaysThePositionStopped(t *testing.T) {
	s := newScenario(t)
	clock := &fakeClockAt{t: time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)}
	w := s.worker(Replace)
	w.Now = clock.now
	w.StallAfter = time.Second
	w.FrozenPositionFactor = 2
	w.Exec = &outFrozenExec{FakeExec: s.exec, clock: clock, tick: time.Second, ticks: 100, frames: true}
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	waitDone(t, done)
	lines, _ := s.store.LogLines(s.id)
	said := false
	for _, l := range lines {
		if l.Section != "encode" {
			continue
		}
		if strings.Contains(l.Text, "its position has not moved for 2s, though it is still writing") {
			said = true
		}
		if strings.Contains(l.Text, "no progress for") {
			t.Errorf("the ceiling's stop says %q; it should say the position has not moved", l.Text)
		}
	}
	if !said {
		t.Error("no log line says the ceiling stopped an encode whose position had not moved for 2s")
	}
}

// A local encode that stopped past twice the source leaves stopOver set on the worker. A remote stall that follows on
// the same worker must not inherit it: its failure is the plain "no progress" stop, not the size wording (#606 a).
func TestRemoteStallDoesNotInheritAnEarlierSizeStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Worker{ID: "t", StallAfter: 40 * time.Millisecond}
	w.current = 7
	w.cancelJob = cancel
	w.stopOver.Store(true) // left by the earlier local stop
	rj := NewRemoteJob("lease")
	rj.ConvertingFrames(1e6, 0, 0, 14, time.Now(), 100) // no frames, bytes or position move: a plain stall
	stopWatch := w.watchRemoteStall(7, rj)
	defer stopWatch()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a remote encode that made no progress was not stopped")
	}
	if w.stopOver.Load() {
		t.Error("the remote stall stopped with the earlier size flag still set: its failure would read as the size stop")
	}
}
