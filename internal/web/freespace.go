// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// A library's free space and write access, read with a time limit: a hung network mount (NFS, SMB on a NAS) can
// make statfs or a write probe wait for minutes, and the library list must not wait with it (review 5053a73).
//   - each read waits at most its timeout; then the last answer (or none) is shown;
//   - a path with a read still running starts no other, so a mount that stays hung holds one goroutine;
//   - an answer is kept for its TTL, and a read that finishes late still fills it in.
var (
	diskFree     = worker.DiskFree // tests replace it
	freeTimeout  = time.Second
	freeTTL      = 30 * time.Second
	writeCheck   = doctor.CanWrite // tests replace it
	writeTimeout = time.Second
	writeTTL     = time.Minute
)

var (
	freeCache  timedCache[*uint64]
	writeCache timedCache[bool]
)

type timedEntry[T any] struct {
	at      time.Time
	val     T
	has     bool // val is an answer (not just the zero value)
	pending bool // a read for this path is running
}

// timedCache keeps one answer per path. get returns the cached answer while it is fresh; otherwise it starts a
// read, waits at most wait for it, and returns the last answer it has if the read is late.
type timedCache[T any] struct {
	mu sync.Mutex
	m  map[string]timedEntry[T]
}

func (c *timedCache[T]) get(path string, read func() T, ttl, wait time.Duration) (T, bool) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]timedEntry[T]{}
	}
	e := c.m[path]
	if e.pending || e.has && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return e.val, e.has
	}
	e.pending = true
	c.m[path] = e
	c.mu.Unlock()

	done := make(chan T, 1)
	go func() {
		v := read()
		c.mu.Lock()
		c.m[path] = timedEntry[T]{at: time.Now(), val: v, has: true}
		c.mu.Unlock()
		done <- v
	}()
	select {
	case v := <-done:
		return v, true
	case <-time.After(wait):
		return e.val, e.has
	}
}

// freeBytes is path's free space, or nil when it isn't known in time.
func freeBytes(path string) *uint64 {
	b, _ := freeCache.get(path, func() *uint64 {
		n, err := diskFree(path)
		if err != nil {
			return nil
		}
		return &n
	}, freeTTL, freeTimeout)
	return b
}

// canWriteTimed says whether the app can write to path. known is false when no answer is in yet (the first read
// is still running): the caller shows nothing then, so an unanswered folder is not reported as read-only.
func canWriteTimed(path string) (writable, known bool) {
	return writeCache.get(path, func() bool { return writeCheck(path) }, writeTTL, writeTimeout)
}
