// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKey is a key the test moves by hand.
type fakeKey struct {
	mu sync.Mutex
	k  string
}

func (f *fakeKey) set(k string) { f.mu.Lock(); f.k = k; f.mu.Unlock() }
func (f *fakeKey) get() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.k, nil
}

// setSWR makes a compute count as slow and lets the gap be short, for the length of the test.
func setSWR(t *testing.T, slow, gap time.Duration) {
	t.Helper()
	oldSlow, oldGap := swrSlow, swrMinGap
	swrSlow, swrMinGap = slow, gap
	t.Cleanup(func() { swrSlow, swrMinGap = oldSlow, oldGap })
}

// swrWaitFor polls cond for up to 5 s.
func swrWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A request gets the last value at once while a slow recompute runs, and then the new one. There is no "live page"
// window: the first ask after a quiet spell is served at once too (QA #421: it took 3-9 s at 200k jobs).
func TestSWRServesTheLastValueWhileItRecomputes(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	var builds atomic.Int32
	build := func() (int, string, error) {
		n := int(builds.Add(1))
		time.Sleep(60 * time.Millisecond) // a slow compute
		k, _ := key.get()
		return n, k, nil
	}
	var c swrCache[int]
	if v, err := c.get(key.get, build); err != nil || v != 1 {
		t.Fatalf("first call: %d, %v; want 1 (computed in the request)", v, err)
	}
	key.set("b")
	start := time.Now()
	v, err := c.get(key.get, build)
	if took := time.Since(start); err != nil || v != 1 || took > 30*time.Millisecond {
		t.Fatalf("during the recompute: %d, %v in %v; want the old 1 at once", v, err, took)
	}
	swrWaitFor(t, "the new value", func() bool {
		v, _ := c.get(key.get, build)
		return v == 2
	})
}

// However many key changes come while a compute runs, there is never more than one compute running, and one
// more afterwards: not a compute per event.
func TestSWRCoalescesKeyChangesIntoOneMoreCompute(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	var running, maxRunning, builds atomic.Int32
	build := func() (int, string, error) {
		builds.Add(1)
		r := running.Add(1)
		for m := maxRunning.Load(); r > m && !maxRunning.CompareAndSwap(m, r); m = maxRunning.Load() {
		}
		time.Sleep(80 * time.Millisecond)
		running.Add(-1)
		k, _ := key.get()
		return int(builds.Load()), k, nil
	}
	var c swrCache[int]
	c.get(key.get, build)     // 1 build, in the request
	for i := 0; i < 50; i++ { // 50 events while the page is live
		key.set(string(rune('b' + i%20)))
		if _, err := c.get(key.get, build); err != nil {
			t.Fatal(err)
		}
	}
	swrWaitFor(t, "the background computes to finish", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.queued
	})
	if m := maxRunning.Load(); m != 1 {
		t.Errorf("up to %d computes ran at once; want 1", m)
	}
	if n := builds.Load(); n > 3 {
		t.Errorf("%d computes for 50 events; want at most the first and one or two after it", n)
	}
}

// With nothing yet, the first call computes in the request and returns the fresh numbers.
func TestSWRFirstCallReturnsFreshNumbers(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	var builds int
	build := func() (string, string, error) {
		builds++
		k, _ := key.get()
		return "fresh " + k, k, nil
	}
	var c swrCache[string]
	v, err := c.get(key.get, build)
	if err != nil || v != "fresh a" || builds != 1 {
		t.Fatalf("first call %q, %v after %d builds; want fresh a after 1", v, err, builds)
	}
	if v, _ := c.get(key.get, build); v != "fresh a" || builds != 1 {
		t.Errorf("same key again: %q after %d builds; want no new build", v, builds)
	}
}

// A failed background recompute keeps the last value, and the next key change tries again.
func TestSWRErrorKeepsTheLastValue(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	var fail atomic.Bool
	var n atomic.Int32
	build := func() (int, string, error) {
		time.Sleep(30 * time.Millisecond)
		if fail.Load() {
			return 0, "", errors.New("disk I/O error")
		}
		return int(n.Add(1)), key.get2(), nil
	}
	var c swrCache[int]
	if _, err := c.get(key.get, build); err != nil { // value 1
		t.Fatal(err)
	}
	fail.Store(true)
	key.set("b")
	if v, err := c.get(key.get, build); err != nil || v != 1 {
		t.Fatalf("during a failing recompute: %d, %v; want the last value 1", v, err)
	}
	swrWaitFor(t, "the failed recompute to finish", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.queued
	})
	if v, err := c.get(key.get, build); err != nil || v != 1 {
		t.Fatalf("after the failure: %d, %v; want still 1", v, err)
	}
	fail.Store(false)
	swrWaitFor(t, "a new value after the next key change", func() bool {
		v, _ := c.get(key.get, build)
		return v == 2
	})
}

// key.get2 is the key without the error, for the build closures above.
func (f *fakeKey) get2() string { k, _ := f.get(); return k }

// A key change while a background compute runs gives one more compute, with no further request: the cache ends on
// the current key, not the one the running compute was for.
func TestSWRKeyChangeDuringAComputeIsNotLost(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	started := make(chan string, 10)
	release := make(chan struct{})
	build := func() (string, string, error) {
		k, _ := key.get() // the key at the start, as the real computes read it
		started <- k
		if k == "b" {
			<-release // hold the compute for "b" until the key has moved on
		} else {
			time.Sleep(20 * time.Millisecond) // slow enough to count as slow
		}
		return k, k, nil
	}
	var c swrCache[string]
	c.get(key.get, build) // "a", in the request
	<-started
	key.set("b")
	c.get(key.get, build) // starts the background compute for "b"
	if k := <-started; k != "b" {
		t.Fatalf("background compute started for %q; want b", k)
	}
	key.set("c")
	c.get(key.get, build) // a compute is running: gets the last value, queues nothing new
	close(release)
	swrWaitFor(t, "the cache to reach key c with no further request", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.key == "c" && !c.queued
	})
}

// With nobody asking, the background computes stop, even though the key keeps moving (it holds the minute).
func TestSWRBackgroundStopsWhenNobodyAsks(t *testing.T) {
	setSWR(t, 10*time.Millisecond, 0)
	var key fakeKey
	key.set("a")
	var builds atomic.Int32
	build := func() (int, string, error) {
		n := builds.Add(1)
		time.Sleep(15 * time.Millisecond)
		return int(n), fmt.Sprintf("minute-%d", n), nil // the key has moved on by the time each compute ends
	}
	var c swrCache[int]
	c.get(key.get, build) // computed in the request
	key.set("b")
	c.get(key.get, build) // one background compute; then nobody asks again
	swrWaitFor(t, "the background compute to stop", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.queued
	})
	n := builds.Load()
	time.Sleep(100 * time.Millisecond)
	if m := builds.Load(); m != n || n > 2 {
		t.Fatalf("%d computes, then %d after 100 ms with nobody asking; want 2 and no more", n, m)
	}
}
