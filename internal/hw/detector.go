// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// Emitter is the event bus as the detector uses it.
type Emitter interface {
	Emit(events.Event) error
}

// Detect proves each candidate in turn, one test at a time (L-HW-1). It stops early when ctx ends.
func Detect(ctx context.Context, m media.Media, dir string, cands []Candidate) Inventory {
	inv := Inventory{At: time.Now().UTC()}
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		inv.Verdicts = append(inv.Verdicts, Prove(ctx, m, dir, c))
	}
	return inv
}

// Detector runs detection in the background (L-HW-3). Start returns at once; Current serves the last finished pass
// until a new pass ends, and a failing pass replaces it like any other.
type Detector struct {
	m     media.Media
	dir   string
	cands []Candidate
	bus   Emitter

	mu      sync.Mutex
	cur     Inventory
	has     bool
	running bool
}

// NewDetector makes a detector over the candidates. bus may be nil, in which case nothing is reported.
func NewDetector(m media.Media, dir string, cands []Candidate, bus Emitter) *Detector {
	return &Detector{m: m, dir: dir, cands: cands, bus: bus}
}

// Start begins a pass in the background and returns at once. A call while a pass is running does nothing.
func (d *Detector) Start(ctx context.Context) {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()
	go func() {
		inv := Detect(ctx, d.m, d.dir, d.cands)
		d.mu.Lock()
		d.cur, d.has, d.running = inv, true, false
		d.mu.Unlock()
		d.report(inv)
	}()
}

// Current returns the last finished pass. ok is false until one has finished.
func (d *Detector) Current() (Inventory, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cur, d.has
}

// report writes the pass as one System log line: the available encoders, and why each other one is not.
func (d *Detector) report(inv Inventory) {
	if d.bus == nil {
		return
	}
	_ = d.bus.Emit(events.Event{
		Kind: events.KindNote, Level: events.LevelInfo, Source: "hw", Code: "encoders.tested",
		Message: Summary(inv),
	})
}

// Summary is the one-line answer for a pass: each available encoder, then each one that is not with its reason.
func Summary(inv Inventory) string {
	var ok, bad []string
	for _, v := range inv.Verdicts {
		if v.Available {
			ok = append(ok, v.Encoder)
		} else {
			bad = append(bad, fmt.Sprintf("%s not available: %s", v.Encoder, v.Reason))
		}
	}
	parts := []string{}
	if len(ok) > 0 {
		parts = append(parts, "available: "+strings.Join(ok, ", "))
	} else {
		parts = append(parts, "no graphics encoder is available")
	}
	parts = append(parts, bad...)
	return strings.Join(parts, "; ")
}
