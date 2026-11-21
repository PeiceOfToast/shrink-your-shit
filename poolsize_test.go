// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

func slots(n int) func() int { return func() int { return n } }

// With "Graphics jobs don't wait for processor jobs" off there are exactly Files at once workers, as today;
// on, each graphics slot gets one more, so a graphics job starts beside busy processor jobs. The extra
// workers take no Files at once place, so the processor jobs stay at ParallelJobs.
func TestPoolSizeWithGraphicsJobsSeparate(t *testing.T) {
	if n := poolSize(web.Settings{ParallelJobs: 2}, slots(1)); n != 2 {
		t.Errorf("off: %d workers, want Files at once only (2)", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, slots(1)); n != 3 {
		t.Errorf("on: %d workers, want Files at once plus the card's slot (3)", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, slots(2)); n != 4 {
		t.Errorf("on with two slots: %d workers, want 4", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, slots(0)); n != 2 {
		t.Errorf("on with no graphics slot: %d workers, want the Files at once workers (2)", n)
	}
	// The largest size both settings allow is the pool's cap: the cap is their sum, so raising either clamp
	// moves it with them instead of silently clamping the pool.
	if n := poolSize(web.Settings{ParallelJobs: worker.MaxParallel, GPUJobsSeparate: true}, slots(worker.MaxGPUSlots)); n != poolCap {
		t.Errorf("at both maxima: %d workers, want the pool's cap (%d)", n, poolCap)
	}
	// With the setting off the card's slots are not read at all: the pool must not wake the doctor's tools
	// read every time it reads its size.
	read := false
	poolSize(web.Settings{ParallelJobs: 2}, func() int { read = true; return 1 })
	if read {
		t.Error("the graphics slots were read with the setting off")
	}
}
