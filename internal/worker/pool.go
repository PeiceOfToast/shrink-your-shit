// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Pool runs as many workers as the parallelJobs setting says (slice 24), and follows a change to it
// without a restart: a new worker starts at once; one too many finishes its current file first.
// Pause all, Stop this file and a removed library's stop go to every worker.
type Pool struct {
	New   func(n int) *Worker // the n-th worker (1, 2, ...)
	Size  func() int          // how many should run now (clamped to 1..MaxParallel)
	Every time.Duration       // how often Size is read again (0: 2 s)

	mu      sync.Mutex
	workers []*member
	next    int
}

type member struct {
	w      *Worker
	retire atomic.Bool
	done   chan struct{}
}

// MaxParallel caps parallelJobs: each conversion uses every core already.
const MaxParallel = 8

// Run keeps the pool at its size until ctx ends, then waits for the workers.
func (p *Pool) Run(ctx context.Context) {
	every := p.Every
	if every <= 0 {
		every = 2 * time.Second
	}
	for {
		p.resize(ctx)
		select {
		case <-ctx.Done():
			p.mu.Lock()
			ws := append([]*member(nil), p.workers...)
			p.mu.Unlock()
			for _, m := range ws {
				<-m.done
			}
			return
		case <-time.After(every):
		}
	}
}

func (p *Pool) resize(ctx context.Context) {
	want := min(max(p.Size(), 1), MaxParallel)
	p.mu.Lock()
	defer p.mu.Unlock()
	// workers asked to retire earlier and since finished leave the list
	live := p.workers[:0]
	for _, m := range p.workers {
		select {
		case <-m.done:
		default:
			live = append(live, m)
		}
	}
	p.workers = live
	active := 0
	for _, m := range p.workers {
		if !m.retire.Load() {
			active++
		}
	}
	// Workers asked to retire that are still here (finishing a file) are taken back first: a new one
	// beside them would make more conversions run at once than parallelJobs (or MaxParallel) allows.
	for _, m := range p.workers {
		if active < want && m.retire.Load() {
			m.retire.Store(false)
			active++
		}
	}
	for ; active < want; active++ {
		p.next++
		m := &member{w: p.New(p.next), done: make(chan struct{})}
		p.workers = append(p.workers, m)
		go func() {
			defer close(m.done)
			for ctx.Err() == nil && !m.retire.Load() {
				ran, err := m.w.RunOne(ctx)
				if err != nil {
					log.Printf("worker %s: %v", m.w.ID, err)
				}
				if !ran {
					select {
					case <-ctx.Done():
					case <-time.After(m.w.Poll):
					}
				}
			}
		}()
	}
	for i := len(p.workers) - 1; i >= 0 && active > want; i-- {
		if !p.workers[i].retire.Load() {
			p.workers[i].retire.Store(true) // finishes its current file, takes no other
			active--
		}
	}
}

func (p *Pool) each(f func(*Worker)) {
	p.mu.Lock()
	ws := append([]*member(nil), p.workers...)
	p.mu.Unlock()
	for _, m := range ws {
		f(m.w)
	}
}

// StopJob stops job id wherever it runs (its library was removed).
func (p *Pool) StopJob(id int64) { p.each(func(w *Worker) { w.StopJob(id) }) }

// StopFile stops job id wherever it runs (Stop this file); it reports whether a worker was running it.
func (p *Pool) StopFile(id int64) bool {
	found := false
	p.each(func(w *Worker) { found = w.StopFile(id) || found })
	return found
}

// PauseNow stops every running job and puts it back in the queue; set the paused flag first, so no
// worker takes a new one.
func (p *Pool) PauseNow() { p.each(func(w *Worker) { w.PauseNow() }) }

// Busy is how many workers are converting a file now, retiring ones included.
func (p *Pool) Busy() int {
	n := 0
	p.each(func(w *Worker) {
		if w.busy() {
			n++
		}
	})
	return n
}

// Running is how many workers are not retiring (for tests and the log).
func (p *Pool) Running() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, m := range p.workers {
		if !m.retire.Load() {
			n++
		}
	}
	return n
}
