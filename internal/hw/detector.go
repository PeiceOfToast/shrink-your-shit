// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"fmt"
	"strconv"
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
	return detect(ctx, m, dir, cands, nil)
}

// detect is Detect, telling onTest each verdict as its test ends. A cancelled test has no verdict, so onTest never
// hears about one.
func detect(ctx context.Context, m media.Media, dir string, cands []Candidate, onTest func(Verdict)) Inventory {
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
		if onTest != nil {
			onTest(v)
		}
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
	pauses  *Pauses // where a run the person asked for records what works; nil records nothing

	mu      sync.Mutex
	base    context.Context // the lifetime a run the person asked for uses; the first Start sets it
	cur     Answer
	has     bool
	running bool
	runs    int
	runID   string // the id of the pass running now, when a person asked for it
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

// WithPauses makes a run the person asked for record each encoder that works there, so its failure count starts
// again. A pause that is already running keeps its time (pause.go's rule for a success); a failed test is no job
// attempt, so it is not recorded at all.
func (d *Detector) WithPauses(p *Pauses) *Detector {
	d.pauses = p
	return d
}

// Start begins a pass in the background and returns at once. A call while a pass is running does nothing. The first
// call's context is also the lifetime of a run the person asks for later (page 10's Test the encoders).
func (d *Detector) Start(ctx context.Context) {
	d.mu.Lock()
	if d.base == nil {
		d.base = ctx
	}
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()
	go d.pass(ctx, "", "")
}

// TestRun starts a pass over the encoders for the worker and returns the run's id at once (page 10's Test the
// encoders). Each encoder tested is one probe event in the log, and the finished pass is what Current serves from
// then on. One run at a time per worker: a second ask while a run is going is that run, so two clicks do not test
// twice. An ask while the startup pass is going answers "", since that pass has no id to promise.
func (d *Detector) TestRun(workerID string) string {
	d.mu.Lock()
	if d.running {
		id := d.runID
		d.mu.Unlock()
		return id
	}
	d.runs++
	d.runID = strconv.Itoa(d.runs)
	id := d.runID
	ctx := d.base
	d.running = true
	d.mu.Unlock()
	go d.pass(ctx, id, workerID)
	return id
}

// pass is one background pass: the tests, the device probes, and the answer Current serves. A run has an id, and
// reports each verdict as a probe event of that run.
func (d *Detector) pass(ctx context.Context, runID, workerID string) {
	if ctx == nil {
		ctx = context.Background()
	}
	var onTest func(Verdict)
	if runID != "" {
		onTest = func(v Verdict) {
			d.probe(v, runID, workerID)
			if v.Available && d.pauses != nil {
				d.recordSuccess(ctx, v, workerID)
			}
		}
	}
	inv := detect(ctx, d.m, d.dir, d.cands, onTest)
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
		d.running, d.runID = false, ""
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	d.cur, d.has, d.running, d.runID = Answer{Inventory: inv, Devices: devices}, true, false, ""
	d.mu.Unlock()
	d.report(inv)
}

// probe is one encoder's result as page 10's probe event: the log line in words, and the run it belongs to in the
// data, so the page can tell two tests of the same encoder apart.
func (d *Detector) probe(v Verdict, runID, workerID string) {
	if d.bus == nil {
		return
	}
	message := v.Encoder + ": passed its test"
	data := map[string]any{"runId": runID, "encoder": v.Encoder, "available": v.Available}
	if !v.Available {
		message = v.Encoder + ": failed its test: " + v.Reason
		data["reason"] = v.Reason
	}
	_ = d.bus.Emit(events.Event{Kind: events.KindProbe, Level: events.LevelInfo, Source: "hw", Code: "encoder.probe",
		WorkerID: workerID, Message: message, Data: data})
}

// recordSuccess tells the pause rules an encoder the person tested works: its failure count starts again. A store
// that cannot be written is said in the log rather than ending the run.
func (d *Detector) recordSuccess(ctx context.Context, v Verdict, workerID string) {
	if _, err := d.pauses.Record(ctx, workerID, v.Family, true); err != nil && d.bus != nil {
		_ = d.bus.Emit(events.Event{Kind: events.KindWarning, Level: events.LevelWarn, Source: "hw",
			Code: "encoder.state", WorkerID: workerID,
			Message: "The encoder test's result could not be saved: " + err.Error()})
	}
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
