// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
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
	proven := map[encode.Family]bool{}
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		v := ProveOn(ctx, m, dir, c, proven[c.Family])
		if ctx.Err() != nil {
			// The pass was cancelled during this test: its answer is not a finding. Drop it, and the pass.
			break
		}
		if v.Available {
			proven[c.Family] = true
		}
		inv.Verdicts = append(inv.Verdicts, v)
	}
	return inv
}

// Answer is one finished pass: the inventory, and the device records for the encoders it found. A
// picker gates the full pipeline and the ten-bit and HDR card rungs on those records (L-HW-11).
type Answer struct {
	Inventory Inventory
	Devices   []DeviceRecord
}

// Detector runs detection in the background (L-HW-3). Start returns at once; Current serves the last finished pass
// until a new pass ends, and a failing pass replaces it like any other.
type Detector struct {
	m       media.Media
	dir     string
	cands   []Candidate
	bus     Emitter
	devices bool

	mu      sync.Mutex
	cur     Answer
	has     bool
	running bool
}

// NewDetector makes a detector over the candidates. bus may be nil, in which case nothing is reported.
func NewDetector(m media.Media, dir string, cands []Candidate, bus Emitter) *Detector {
	return &Detector{m: m, dir: dir, cands: cands, bus: bus}
}

// WithDevices makes a pass probe each encoder it finds, so Current also answers what each card can
// really do (L-HW-2). Without it a pass keeps the inventory only.
func (d *Detector) WithDevices() *Detector {
	d.devices = true
	return d
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
		var devices []DeviceRecord
		if d.devices && ctx.Err() == nil {
			for _, v := range inv.Verdicts {
				if ctx.Err() != nil {
					break
				}
				if !v.Available {
					continue
				}
				devices = append(devices, Probe(ctx, d.m, d.dir, v.Candidate))
			}
		}
		if ctx.Err() != nil {
			// A cancelled pass is incomplete: the last finished pass stays.
			d.mu.Lock()
			d.running = false
			d.mu.Unlock()
			return
		}
		d.mu.Lock()
		d.cur, d.has, d.running = Answer{Inventory: inv, Devices: devices}, true, false
		d.mu.Unlock()
		d.report(inv)
	}()
}

// Current returns the last finished pass. ok is false until one has finished.
func (d *Detector) Current() (Answer, bool) {
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
