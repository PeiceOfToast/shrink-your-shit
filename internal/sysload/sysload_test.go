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
	time.Sleep(50 * time.Millisecond)
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
