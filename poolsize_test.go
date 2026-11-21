// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// With "Graphics jobs don't wait for processor jobs" off there are exactly Files at once workers, as today;
// on, each graphics slot gets one more, so a graphics job starts beside busy processor jobs. The extra
// workers take no Files at once place, so the processor jobs stay at ParallelJobs.
func TestPoolSizeWithGraphicsJobsSeparate(t *testing.T) {
	if n := poolSize(web.Settings{ParallelJobs: 2}, 1); n != 2 {
		t.Errorf("off: %d workers, want Files at once only (2)", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, 1); n != 3 {
		t.Errorf("on: %d workers, want Files at once plus the card's slot (3)", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, 2); n != 4 {
		t.Errorf("on with two slots: %d workers, want 4", n)
	}
	if n := poolSize(web.Settings{ParallelJobs: 2, GPUJobsSeparate: true}, 0); n != 2 {
		t.Errorf("on with no graphics slot: %d workers, want the Files at once workers (2)", n)
	}
}
