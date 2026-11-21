// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// The graphics card converts one file at a time unless the person lets it take more (Workers page, the
// graphics slot limit). All of this program's own workers share the one card, so the count is one for the
// program, not one per worker (user bug 11-20: two QSV jobs ran at once with the limit at 1).
var gpuSlots = struct {
	sync.Mutex
	running int
	freed   chan struct{} // closed and replaced when a slot frees
}{freed: make(chan struct{})}

// GPURunning is how many jobs on this computer encode on its graphics card now (the Workers page's
// graphics "used").
func GPURunning() int {
	gpuSlots.Lock()
	defer gpuSlots.Unlock()
	return gpuSlots.running
}

// GPUSlot is the place a job holds on the graphics card while it encodes with it. Release it as soon as
// the graphics encode ends: a job that falls back to the processor does not hold it while the processor
// runs. Release may be called more than once.
type GPUSlot struct {
	once sync.Once
}

// Release gives the slot back, waking every job waiting for one.
func (s *GPUSlot) Release() {
	s.once.Do(func() {
		gpuSlots.Lock()
		gpuSlots.running--
		close(gpuSlots.freed)
		gpuSlots.freed = make(chan struct{})
		gpuSlots.Unlock()
	})
}

// takeGPUSlot waits while the graphics card's slots are all in use and then takes one, or returns nil
// when there are none to take: the person set the limit to 0 (the processor does the encoding), or this
// worker has no limit (a test's). The error is the job being stopped while it waited.
func (w *Worker) takeGPUSlot(ctx context.Context, job jobs.Job) (*GPUSlot, error) {
	if w.GPUSlots == nil { // no limit set: the card takes every job, and the count still covers them all
		return w.takeAnyGPUSlot(), nil
	}
	limit := w.GPUSlots()
	if limit <= 0 {
		w.jobf(job.ID, ": the graphics slots are set to 0, so the processor does the encoding")
		return nil, nil
	}
	said := false
	for {
		gpuSlots.Lock()
		if gpuSlots.running < limit {
			gpuSlots.running++
			gpuSlots.Unlock()
			return &GPUSlot{}, nil
		}
		freed, n := gpuSlots.freed, gpuSlots.running
		gpuSlots.Unlock()
		if !said {
			said = true
			w.jobf(job.ID, ": waiting for a graphics slot (%d of %d in use)", n, limit)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-freed:
		}
	}
}

// takeAnyGPUSlot takes a place on the card with no limit (see takeGPUSlot).
func (w *Worker) takeAnyGPUSlot() *GPUSlot {
	gpuSlots.Lock()
	gpuSlots.running++
	gpuSlots.Unlock()
	return &GPUSlot{}
}
