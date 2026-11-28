// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// M1: each restore recovery finished gets one History line on its own job, with the sentence a live restore writes.
// Red-first: without the function the lines are not built, and this test cannot pass.
func TestRecoveredRestoresGetOneHistoryLineEach(t *testing.T) {
	evs := recoveredRestoreEvents([]int64{7, 9})
	if len(evs) != 2 {
		t.Fatalf("events = %d, want one per restored job", len(evs))
	}
	for i, want := range []int64{7, 9} {
		e := evs[i]
		if e.JobID != want || e.Kind != events.KindRestored || e.Message != safety.RestoredSentence || e.Code != "replace.restored" {
			t.Errorf("event %d = %+v, want the restored line on job %d", i, e, want)
		}
	}
}

// Nothing recovered, nothing written.
func TestNoRecoveredRestoreWritesNoLine(t *testing.T) {
	if evs := recoveredRestoreEvents(nil); len(evs) != 0 {
		t.Errorf("events = %+v, want none", evs)
	}
}
