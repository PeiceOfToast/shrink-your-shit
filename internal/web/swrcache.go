// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"log"
	"sync"
	"time"
)

// swrCache keeps the last value computed for a page's numbers and serves it while a recompute runs. On a large
// store the numbers take seconds, and a live page asks for them again on every event, so:
//   - a request with no value yet, or from a page nobody has asked for in swrAskWindow, computes in the request;
//   - a live page whose last compute was slow (swrSlow or more) gets the last value at once, and when its key has
//     changed starts ONE background recompute; a fast one is computed in the request, so a small store is never stale;
//   - at most one compute runs at a time, and at most one more is queued (a key change during a compute gives one
//     more compute afterwards, not one per event);
//   - background computes start at least max(swrMinGap, 2 x the last compute) apart, so a busy store isn't kept busy;
//   - an error in a background compute is logged; the last value stays, and the next request tries again.
//
// The zero value is ready to use.
type swrCache[T any] struct {
	mu     sync.Mutex // guards the fields below; never held across a compute
	have   bool
	key    string
	want   string // the key of the last request: a background compute runs again until key catches up
	val    T
	asked  time.Time // the last request
	queued bool      // a background compute is waiting or running
	ran    time.Time // when the last compute started
	took   time.Duration
	one    sync.Mutex // one compute at a time
}

// Variables, so the tests can make a compute slow or the gaps short.
var (
	swrAskWindow = 2 * time.Minute        // a page asked this recently is live
	swrSlow      = 250 * time.Millisecond // a compute this slow or slower is recomputed in the background
	swrMinGap    = 10 * time.Second       // the least time between two background computes
)

// get returns the value for the current key. key is cheap; build computes the value and the key it is for.
func (c *swrCache[T]) get(key func() (string, error), build func() (T, string, error)) (T, error) {
	var zero T
	k, err := key()
	if err != nil {
		return zero, err
	}
	now := time.Now()
	c.mu.Lock()
	live := c.have && now.Sub(c.asked) <= swrAskWindow
	c.asked = now
	c.want = k
	if c.have && c.key == k {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	if c.have && live && c.took >= swrSlow {
		c.startLocked(build)
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	return c.computeNow(k, build)
}

// warm computes the value once in the background, at start, so the first page view is not the slow one.
func (c *swrCache[T]) warm(key func() (string, error), build func() (T, string, error)) {
	go func() {
		k, err := key()
		if err == nil {
			_, err = c.computeNow(k, build)
		}
		if err != nil {
			log.Printf("numbers: warming the cache failed: %v", err)
		}
	}()
}

// computeNow computes the value in the caller's request, unless a compute that just finished already has it.
func (c *swrCache[T]) computeNow(k string, build func() (T, string, error)) (T, error) {
	c.one.Lock()
	defer c.one.Unlock()
	c.mu.Lock()
	if c.have && c.key == k {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	return c.run(build)
}

// run computes the value and stores it. The caller holds one.
func (c *swrCache[T]) run(build func() (T, string, error)) (T, error) {
	start := time.Now()
	c.mu.Lock()
	c.ran = start
	c.mu.Unlock()
	v, key, err := build()
	took := time.Since(start)
	c.mu.Lock()
	c.took = took
	if err == nil {
		c.have, c.key, c.val = true, key, v
	}
	c.mu.Unlock()
	return v, err
}

// startLocked starts the background recompute unless one is already queued or running. The caller holds mu.
func (c *swrCache[T]) startLocked(build func() (T, string, error)) {
	if c.queued {
		return
	}
	c.queued = true
	wait := c.waitLocked()
	go func() {
		for {
			if wait > 0 {
				time.Sleep(wait)
			}
			c.one.Lock()
			_, err := c.run(build)
			c.one.Unlock()
			if err != nil {
				log.Printf("numbers: background recompute failed, keeping the last ones: %v", err)
			}
			c.mu.Lock()
			if err == nil && c.key != c.want { // the key moved while it ran: one more, or the page keeps old numbers
				wait = c.waitLocked()
				c.mu.Unlock()
				continue
			}
			c.queued = false
			c.mu.Unlock()
			return
		}
	}()
}

// waitLocked is how long the next background compute waits: max(swrMinGap, 2 x the last compute) after the last
// one started. The caller holds mu.
func (c *swrCache[T]) waitLocked() time.Duration {
	wait := swrMinGap
	if 2*c.took > wait {
		wait = 2 * c.took
	}
	if !c.ran.IsZero() {
		wait -= time.Since(c.ran)
	}
	return wait
}
