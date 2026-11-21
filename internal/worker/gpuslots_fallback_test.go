// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// When the graphics encoder fails, the job gives up its card's place before the processor retries the file: the
// card is free for the next job while the processor converts (#532). Limit 1, so a held place would show as 1.
func TestGraphicsSlotIsFreeDuringProcessorFallback(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_nvenc"
	w := graphicsSlotWorker(s, "t1") // limit 1
	calls := 0
	during := -1
	s.onEncode = func() {
		calls++
		if calls == 2 { // the processor's retry is converting the file now
			during = GPURunning()
		}
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if calls != 2 {
		t.Fatalf("encodes %d, want the graphics encode then the processor", calls)
	}
	if during != 0 {
		t.Fatalf("graphics slots in use while the processor retries: %d, want 0", during)
	}
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics slots in use after the job: %d, want 0", n)
	}
}
