// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package sysload

import (
	"math"
	"runtime"
	"testing"
	"time"
)

// The first sample has memory and no processor figure; the next has both (Linux).
func TestMeter(t *testing.T) {
	var m Meter
	first := m.Sample()
	if runtime.GOOS != "linux" {
		if first.CPUPercent != nil || first.MemTotalBytes != nil {
			t.Error("not measured here: nil")
		}
		return
	}
	if first.CPUPercent != nil || first.MemTotalBytes == nil || *first.MemTotalBytes == 0 || *first.MemUsedBytes > *first.MemTotalBytes {
		t.Fatalf("first %+v", first)
	}
	time.Sleep(minInterval + 50*time.Millisecond) // the next figure needs a gap longer than a short sample
	next := m.Sample()
	if next.CPUPercent == nil || *next.CPUPercent < 0 || *next.CPUPercent > 100 {
		t.Errorf("next cpu %v", next.CPUPercent)
	}
}

// Review da46389: a processor figure stays within 0..100; NaN or an infinity is not measured.
func TestClamp(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for in, want := range map[float64]float64{-3: 0, 42.5: 42.5, 140: 100} {
		if got := Clamp(f(in)); got == nil || *got != want {
			t.Errorf("Clamp(%v) = %v", in, got)
		}
	}
	for _, in := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if Clamp(f(in)) != nil {
			t.Errorf("Clamp(%v) should be nil", in)
		}
	}
	if Clamp(nil) != nil {
		t.Error("nil stays nil")
	}
}

// ui-shop QA (P3): two polls a few ms apart (the meter is shared) see about one tick of processor time, all of it
// busy by the arithmetic, and read 100% on an idle box. A short interval is not measured: it returns the last figure
// and leaves the baseline where it was, so the next long-enough interval still measures from the old baseline.
func TestMeterShortIntervalIsNotMeasured(t *testing.T) {
	var at time.Time
	var idle, total uint64
	oldClock, oldRead := clock, readCPU
	clock = func() time.Time { return at }
	readCPU = func() (uint64, uint64, bool) { return idle, total, true }
	t.Cleanup(func() { clock, readCPU = oldClock, oldRead })

	var m Meter
	t0 := time.Unix(1000, 0)
	at, idle, total = t0, 1000, 1000
	m.Sample()                                               // the baseline
	at, idle, total = t0.Add(5*time.Millisecond), 1000, 1001 // one tick, and none of it idle
	if l := m.Sample(); l.CPUPercent != nil {
		t.Errorf("a 5 ms sample reported %v%%; want not measured", *l.CPUPercent)
	}
	at, idle, total = t0.Add(1005*time.Millisecond), 1900, 2000 // 1000 ticks, 900 idle, from the first baseline
	l := m.Sample()
	if l.CPUPercent == nil || math.Abs(*l.CPUPercent-10) > 0.01 {
		t.Errorf("cpu %v after a long interval; want 10 measured from the first baseline", l.CPUPercent)
	}
}
