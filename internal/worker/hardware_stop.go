// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"syscall"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The words a job fails with when the Convert step's hardware choice stops it. They are plain, and the job is not
// retried. Local and remote workers say the same thing.
const (
	HardwareOnlyNoEncoderWords = "Hardware only: no graphics encoder took this file, and this step never uses the processor. Your original was not touched."
	HardwareOnlyFailedWords    = "Hardware only: the graphics encoder failed, and this step never uses the processor. Your original was not touched."
	HardwareFailFailedWords    = "The graphics encoder failed, and this step is set to fail rather than convert on the processor. Your original was not touched."
	HardwareOnlySlotsWords     = "Hardware only: the graphics slots are set to 0, so no graphics encoder may run, and this step never uses the processor. Your original was not touched."
	HardwarePausedWords        = "The graphics encoder is paused after three failures in a row. This step does not convert on the processor while it is paused, so the job waits and tries again. If it still can't, press Retry once the card is back. Your original was not touched."
)

// HardwareStopWords is the plain words for a graphics encoder's failure that the Convert step's hardware choice
// stops: under only the job never converts on the processor, and under fail it never steps down. "" when the choice
// does not stop it: the default steps down as before, and a failure the system (killed) or the disk (no space)
// made keeps its own retry.
func HardwareStopWords(v rules.VideoEncode, err error) string {
	if KilledBySignal(err) || errors.Is(err, syscall.ENOSPC) {
		return ""
	}
	switch {
	case v.Hardware == "only":
		return HardwareOnlyFailedWords
	case v.HardwareFail == "fail":
		return HardwareFailFailedWords
	}
	return ""
}
