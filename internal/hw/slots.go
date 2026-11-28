// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"errors"
	"sync"
)

// ErrNoGraphicsSlots is a job asking for a place on a card the person gave no places to (the graphics slots
// are 0). The job goes to the processor and says why.
var ErrNoGraphicsSlots = errors.New("graphics slots are set to 0")

// The graphics card converts one file at a time unless the person lets it take more (Workers page, graphics
// slots). The count is one for the whole process: every slot and every built-in worker shares this computer's
// card, so it is the card that is limited, not a slot (L-HW-8).
var slots = struct {
	sync.Mutex
	running int
	freed   chan struct{} // closed and replaced when a place comes free
}{freed: make(chan struct{})}

// Running is how many jobs encode on the graphics card now. A job waiting for a place is not counted.
func Running() int {
	slots.Lock()
	defer slots.Unlock()
	return slots.running
}

// Slot is a place on the graphics card while a job encodes with it. Give it back as soon as the card's encode
// ends: a job the processor finishes does not hold a place. Release may be called more than once (L-HW-8).
type Slot struct {
	once sync.Once
}

// Release gives the place back, waking every job waiting for one. Calling it twice is harmless.
func (s *Slot) Release() {
	s.once.Do(func() {
		slots.Lock()
		slots.running--
		close(slots.freed)
		slots.freed = make(chan struct{})
		slots.Unlock()
	})
}

// TakeSlot waits while the card's places are all in use and then takes one. limit is the worker's graphics
// slots: 0 means no card encode at all, which reports ErrNoGraphicsSlots so the caller can put the job on the
// processor and say why; a negative limit means no limit is set (the card takes every job, still counted). The
// error is the job being stopped while it waited.
func TakeSlot(ctx context.Context, limit int) (*Slot, error) {
	if limit == 0 {
		return nil, ErrNoGraphicsSlots
	}
	if limit < 0 {
		slots.Lock()
		slots.running++
		slots.Unlock()
		return &Slot{}, nil
	}
	for {
		slots.Lock()
		if slots.running < limit {
			slots.running++
			slots.Unlock()
			return &Slot{}, nil
		}
		freed := slots.freed
		slots.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-freed:
		}
	}
}
