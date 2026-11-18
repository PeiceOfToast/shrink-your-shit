// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// A library's free space, read with a time limit: a hung network mount (NFS, SMB on a NAS) can make
// statfs wait for minutes, and the library list must not wait with it (review 5053a73).
//   - each read waits at most freeTimeout; then the last answer (or none: null) is shown;
//   - a path with a read still running starts no other, so a mount that stays hung holds one goroutine;
//   - an answer is kept for freeTTL, and a read that finishes late still fills it in.
var (
	diskFree    = worker.DiskFree // tests replace it
	freeTimeout = time.Second
	freeTTL     = 30 * time.Second
)

type freeEntry struct {
	at      time.Time
	bytes   *uint64
	pending bool
}

var freeCache = struct {
	sync.Mutex
	m map[string]freeEntry
}{m: map[string]freeEntry{}}

// freeBytes is path's free space, or nil when it isn't known in time.
func freeBytes(path string) *uint64 {
	freeCache.Lock()
	e := freeCache.m[path]
	if e.pending || !e.at.IsZero() && time.Since(e.at) < freeTTL {
		freeCache.Unlock()
		return e.bytes
	}
	e.pending = true
	freeCache.m[path] = e
	freeCache.Unlock()

	done := make(chan *uint64, 1)
	go func() {
		var b *uint64
		if n, err := diskFree(path); err == nil {
			b = &n
		}
		freeCache.Lock()
		freeCache.m[path] = freeEntry{at: time.Now(), bytes: b}
		freeCache.Unlock()
		done <- b
	}()
	select {
	case b := <-done:
		return b
	case <-time.After(freeTimeout):
		return e.bytes // the last answer, if any
	}
}
