// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A finishing report that answers lease_gone must not deadlock the tick: convert's send calls lose (which takes the
// reports' lock) and cancels the encode. Under the old tick, send ran under that lock, so lose never returned.
func TestFinishingReportAnsweredLeaseGoneDoesNotDeadlockTheTick(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.tmp.mkv")
	if err := os.WriteFile(out, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := &encodeReports{label: "cpu", out: out, duration: 10}
	rep.moved(10e6) // the position is at the file's length: the finishing step
	sent := make(chan struct{}, 1)
	errGone := errors.New("lease gone")
	send := func(map[string]any) { // convert's send, with a report that answers lease_gone
		select {
		case sent <- struct{}{}:
		default:
		}
		rep.lose(errGone)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); rep.tick(stop, time.Millisecond, send) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("no finishing report was sent")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tick did not return after a lease_gone answer: the encode would never be cancelled")
	}
	if rep.lost() == nil {
		t.Error("the lease loss was not recorded")
	}
}

// Once progress=end is read, no finishing report is sent, and none is sent after end returns: the "checking" report
// that follows it is the last one.
func TestNoFinishingReportAfterEnd(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.tmp.mkv")
	if err := os.WriteFile(out, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := &encodeReports{label: "cpu", out: out, duration: 10}
	rep.moved(10e6)
	var mu sync.Mutex
	var sends int
	send := func(map[string]any) { mu.Lock(); sends++; mu.Unlock() }
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); rep.tick(stop, time.Millisecond, send) }()
	time.Sleep(20 * time.Millisecond)
	rep.end()
	mu.Lock()
	atEnd := sends
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if sends != atEnd {
		t.Errorf("%d finishing reports after progress=end, want 0", sends-atEnd)
	}
}
