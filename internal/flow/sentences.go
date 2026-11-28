// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"fmt"
	"math"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The sentences a job's log shows for its events. The Job log is what the person reads at stop 1, so each event has
// one plain line, built here from the facts the engine already has.

// stepSentence is a step's line: its number on the pages and its name, as the Start page counts them.
func stepSentence(index int, name string) string {
	return fmt.Sprintf("%d. %s", index, name)
}

// probeSentence summarises a file: its first movie video track's codec and size, then its duration in seconds.
// Cover art is not the movie, so it is skipped.
func probeSentence(info media.Info) string {
	var parts []string
	for _, v := range info.Video {
		if v.AttachedPicture {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d×%d", codecName(v.Codec), v.Width, v.Height))
		break
	}
	if d := info.Format.Duration; d > 0 {
		parts = append(parts, fmt.Sprintf("%s s", trimFloat(d.Seconds())))
	}
	return strings.Join(parts, ", ")
}

// codecName is the name a person knows a codec by.
func codecName(c string) string {
	switch strings.ToLower(c) {
	case "h264":
		return "H.264"
	case "hevc", "h265":
		return "HEVC"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "mpeg4":
		return "MPEG-4"
	}
	return strings.ToUpper(c)
}

// sizeSentence is a size change: before → after, and the saving in percent when there was a before.
func sizeSentence(before, after int64) string {
	s := humanBytes(before) + " → " + humanBytes(after)
	if before > 0 {
		pct := math.Round(float64(after-before) * 100 / float64(before))
		s += fmt.Sprintf(" (%+.0f %%)", pct)
	}
	return s
}

// humanBytes writes a size in KB, MB or GB, one decimal place, as the pages do.
func humanBytes(n int64) string {
	const k = 1000.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f GB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f KB", f/k)
	}
	return fmt.Sprintf("%d B", n)
}

// trimFloat writes a number without trailing zeros: 4 for 4.0, 3.5 for 3.5.
func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}

// The next-action sentences: the failure card's line saying what to do now (page 03, S3-6a). The job row keeps the
// sentence, not a code, because the page shows it as it is. Each one ends in the action the person can take; a
// workflow fix ends "then press Retry", so the button the page shows is the one the sentence names.
const (
	// NextRetry is for a failure where trying the file again is the whole answer.
	NextRetry = "Press Retry to try the file again."
	// NextRetryLog is for the encoder stopping: the log is where a second failure is explained.
	NextRetryLog = "Press Retry to try the file again; if it fails again, the job log has the details."
	// NextTriedAgain is for a may-pass: the worker tries again on its own in a few minutes.
	NextTriedAgain = "This file will be tried again in a few minutes."
	// NextFixWorkflow is for a flow that does not fit this file: the person changes it, then retries.
	NextFixWorkflow = "Change the workflow so it fits this file, then press Retry."
	// NextOpenWorkflow is for a saved workflow the engine cannot run at all.
	NextOpenWorkflow = "Open the workflow and fix what it reports, then press Retry."
	// NextFixSettings is for a step whose saved settings could not be read.
	NextFixSettings = "Fix the step settings in the workflow, then press Retry."
	// NextFreeName is for a new file whose name another file took.
	NextFreeName = "Another file has the new file's name. Move or rename it, then press Retry."
	// NextMoveSrt is for a subtitle file's name taken before anything was written.
	NextMoveSrt = "Move or rename that .srt file, then press Retry."
	// NextDestination is for the save step failing to write or read its own staging.
	NextDestination = "Check the destination folder, then press Retry."
	// NextEncoder is for no encoder being available on the worker that ran the job.
	NextEncoder = "Check the video encoder on the computer that ran this job, then press Retry."
	// NextFileReadable is for a file that cannot be read or identified at all.
	NextFileReadable = "Check that the file is still there and readable, then press Retry."
	// NextWorker is for the worker failing to build the run before flow starts (the file or its workflow is gone).
	NextWorker = "Check that the file and its workflow are still there, then press Retry."
	// NextBug is for an internal error: the person can do nothing about it but send the log.
	NextBug = "This is a bug, not a problem with your file. Download this job's log and report it."
)

// nextForPlan is the sentence for a step's plan refusing: a saved workflow or a step's settings that need a look,
// or a flow that does not fit this file.
func nextForPlan(code string) string {
	switch code {
	case "invalid_flow":
		return NextOpenWorkflow
	case "invalid_settings":
		return NextFixSettings
	}
	return NextFixWorkflow
}
