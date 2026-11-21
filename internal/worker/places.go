// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// errNoFilePlace is a job handed back to the queue before it started: it would convert on the processor and
// every Files-at-once place is in use, so the worker that planned it was there to find a graphics job instead.
var errNoFilePlace = errors.New("every Files at once place is busy")

// maxSkipAhead is how many jobs a worker hands back while looking past them for a graphics job; past it the
// worker waits for a place to free instead. Bounded so a long queue of processor jobs is not planned again
// and again by every worker beside them.
const maxSkipAhead = 16

// filePlaces are the program's Files-at-once places: how many of its jobs may convert on the processor at
// once. With "Graphics jobs don't wait for processor jobs" on, a job that converts on the graphics card
// holds none of them, so it runs beside the processor jobs; with the setting off no worker takes one, and
// the pool's own workers are the places (today's behaviour). All of the program's workers share them, like
// the card's slots.
var filePlaces = struct {
	sync.Mutex
	running int
	freed   chan struct{} // closed and replaced when a place is given back
}{freed: make(chan struct{})}

// filePlace is one Files-at-once place, held while a job converts on the processor. Release may be called
// more than once.
type filePlace struct {
	once sync.Once
}

// Release gives the place back, waking every job waiting for one.
func (p *filePlace) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		filePlaces.Lock()
		filePlaces.running--
		close(filePlaces.freed)
		filePlaces.freed = make(chan struct{})
		filePlaces.Unlock()
	})
}

// tryFilePlace takes a place if one is free now; nil when they are all in use.
func tryFilePlace(limit int) *filePlace {
	filePlaces.Lock()
	defer filePlaces.Unlock()
	if filePlaces.running >= limit {
		return nil
	}
	filePlaces.running++
	return &filePlace{}
}

// filePlacesBusy reports whether every one of the limit places is in use.
func filePlacesBusy(limit int) bool {
	filePlaces.Lock()
	defer filePlaces.Unlock()
	return filePlaces.running >= limit
}

// FilePlacesInUse is how many of the program's Files-at-once places jobs hold now.
func FilePlacesInUse() int {
	filePlaces.Lock()
	defer filePlaces.Unlock()
	return filePlaces.running
}

// filePlaceLimit is how many Files-at-once places this worker's jobs take: 0 when the setting is off (the
// pool's own workers are the places, as before), or nil.
func (w *Worker) filePlaceLimit() int {
	if w.FilePlaces == nil {
		return 0
	}
	return w.FilePlaces()
}

// takeFilePlace waits for a free place and takes it. A graphics job that fell back to the processor waits
// here, so it never runs beside more processor jobs than Files at once allows. The error is the job being
// stopped while it waited.
func (w *Worker) takeFilePlace(ctx context.Context, job jobs.Job) (*filePlace, error) {
	limit := w.filePlaceLimit()
	if limit <= 0 {
		return nil, nil // no such places (the setting is off): the pool's own workers are the limit, exactly as before
	}
	said := false
	for {
		if p := tryFilePlace(limit); p != nil {
			return p, nil
		}
		filePlaces.Lock()
		freed, n := filePlaces.freed, filePlaces.running
		filePlaces.Unlock()
		if !said {
			said = true
			w.jobf(job.ID, ": waiting for a Files at once place (%d of %d in use)", n, limit)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-freed:
		}
	}
}
