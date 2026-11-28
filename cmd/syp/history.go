// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// recoveredRestoreEvents is the History line for each restore that recovery finished, one per job (review M1): the
// same line a live restore writes, so a restore a crash interrupted reads the same as one that did not.
func recoveredRestoreEvents(jobs []int64) []events.Event {
	out := make([]events.Event, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, events.Event{JobID: job, Level: events.LevelInfo, Kind: events.KindRestored,
			Source: "safety", Code: "replace.restored", Message: safety.RestoredSentence})
	}
	return out
}
