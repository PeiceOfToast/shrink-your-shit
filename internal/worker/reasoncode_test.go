// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"syscall"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Every failure, kept original and retry has a fixed code from jobs.JobReasonCodes, never its sentence
// (ui-shop, cap-778a2f0: low space gave code = "there is not enough free disk space ...").
func TestEveryReasonCodeIsAKnownOne(t *testing.T) {
	lowSpace := kept("there is not enough free disk space next to this file").(*KeptError)
	lowSpace.Code = jobs.ReasonLowDiskSpace
	for err, want := range map[error]string{
		retryable(lowSpace, retrySpace):                           jobs.ReasonLowDiskSpace,
		kept("the new file does not look like the original"):      jobs.ReasonKeptOriginal,
		retryable(kept("could not be checked"), retryGeneral):     jobs.ReasonKeptOriginal,
		failed(diskFull, syscall.ENOSPC):                          jobs.ReasonDiskFull,
		retryable(failed(diskFull, errors.New("x")), retrySpace):  jobs.ReasonDiskFull,
		failed(cannotRead, fs.ErrPermission):                      jobs.ReasonCannotRead,
		failed("The file is not there any more.", fs.ErrNotExist): jobs.ReasonConvertFailed,
		fmt.Errorf("write: %w", syscall.ENOSPC):                   jobs.ReasonDiskFull,
		fmt.Errorf("create: %w", fs.ErrPermission):                jobs.ReasonCannotWrite,
		errors.New("exit status 1"):                               jobs.ReasonConvertFailed,
	} {
		got := reasonCode(err)
		if got != want || !slices.Contains(jobs.JobReasonCodes, got) {
			t.Errorf("%v: code %q, want %q (a known one)", err, got, want)
		}
	}
	if !slices.Contains(jobs.JobReasonCodes, jobs.ReasonStalled) {
		t.Error("stalled is not listed")
	}
}
