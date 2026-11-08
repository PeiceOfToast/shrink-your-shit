// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"os/exec"
	"time"
)

// Failures that may pass (DD-2): a slow or busy disk, a full one, ffmpeg stopped by the system, an
// original still being written. They are tried again later, a few times, each try from the start (the
// original never touched); the rest end at once with their plain reason.

// retryKind is how long a retryable failure waits.
type retryKind int

const (
	retryGeneral retryKind = iota // something busy or slow: minutes
	retrySpace                    // the disk is full: space frees up over hours
)

// maxTries is how many tries a job gets in all; retryWaits[kind][n] is the wait after the n-th failed one.
// A variable only so tests of a single try's outcome can set it to 1.
var maxTries = 3

var retryWaits = map[retryKind][]time.Duration{
	retryGeneral: {5 * time.Minute, 30 * time.Minute},
	retrySpace:   {30 * time.Minute, 2 * time.Hour},
}

// retryableError marks a failure (a *FailedError or *KeptError, whose plain words stay) as one that may pass.
type retryableError struct {
	err  error
	kind retryKind
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

func retryable(err error, kind retryKind) error {
	if err == nil {
		return nil
	}
	return &retryableError{err, kind}
}

// retryIn reports whether err may pass and the job (attempts tries finished before this one) has a try
// left, and how long to wait for it.
func retryIn(err error, attempts int) (time.Duration, bool) {
	var re *retryableError
	if !errors.As(err, &re) || attempts+1 >= maxTries {
		return 0, false
	}
	waits := retryWaits[re.kind]
	return waits[min(attempts, len(waits)-1)], true
}

// killedBySignal reports whether a program ended because the system stopped it (out of memory, killed),
// not because it gave up on the file.
func killedBySignal(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ProcessState != nil && !ee.Exited()
}
