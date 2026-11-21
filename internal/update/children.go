// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"errors"
	"time"
)

// endChildren ends each of kids with stop, even when one fails or the walk that found them failed partway, and
// returns every error, the walk's last.
func endChildren(kids []int, walkErr error, stop func(int) error) error {
	var errs []error
	for _, k := range kids {
		if err := stop(k); err != nil {
			errs = append(errs, err)
		}
	}
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return errors.Join(errs...)
}

// childCandidate is a process whose parent PID is the app's: when it was created, or why that could not be read.
type childCandidate struct {
	pid     int
	created time.Time
	err     error
}

// ownChildren picks the candidates the app started: not keep, and created no earlier than the app. Windows reuses
// PIDs and never clears a dead parent's, so a process can name the app as its parent without being its child. A
// process whose creation time cannot be read is not provably the app's, so it is left running.
func ownChildren(cands []childCandidate, app time.Time, keep int) []int {
	var kids []int
	for _, c := range cands {
		if c.pid == keep || c.err != nil || c.created.Before(app) {
			continue
		}
		kids = append(kids, c.pid)
	}
	return kids
}
