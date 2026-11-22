// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Rung is one try of an attempt: its arguments, the graphics encoder that makes the file ("" for the processor)
// and whether it runs the full GPU pipeline (the card decodes too).
type Rung struct {
	Args    []string
	Encoder string
	Full    bool
}

// AttemptInput is what an attempt steps down from: the plan and the paths for the lower rungs, and the first rung
// the caller chose (the card's slot, if any, is the caller's).
type AttemptInput struct {
	Plan    rules.Plan
	SrcPath string
	Tmp     string
	First   Rung
}

// RunFunc runs one rung's arguments. stderr is the encode's log and err its end. It is the only part that differs
// between the local worker and the remote one.
type RunFunc func(ctx context.Context, args []string) (stderr string, err error)

// AttemptHooks are all optional.
type AttemptHooks struct {
	// OnRung is called before each rung after the first runs: the live label, and the card's slot released when the
	// processor takes over (Encoder "").
	OnRung func(r Rung)
	// Failed is called for a rung that steps down, with its error lines and err, before the partial file is cleared.
	Failed func(r Rung, errText string, err error)
	// Note writes a job-log line; the caller adds its own prefix.
	Note func(text string)
	// Stopped says the worker stopped the encode (the stall stop): a stop never steps down.
	Stopped func() bool
}

// AttemptResult is the last rung that ran.
type AttemptResult struct {
	Rung              // the rung that ran last; Encoder "" is the processor
	Stderr     string // the encode's raw log
	Errors     string // its error lines
	Err        error
	Fell       bool   // the graphics encoder failed and the processor converted the file
	FellReason string // the failure's first line, shortened
}

// Attempt runs the conversion down the ladder: the full GPU pipeline, then the hybrid (the processor decodes for the
// graphics encoder), then the processor. A graphics rung that fails steps down one rung when attemptSteps says so.
func Attempt(ctx context.Context, in AttemptInput, run RunFunc, hooks AttemptHooks) AttemptResult {
	r, fell, fellReason := in.First, false, ""
	for {
		raw, err := run(ctx, r.Args)
		res := AttemptResult{Rung: r, Stderr: raw, Errors: media.ErrorLines([]byte(raw)), Err: err, Fell: fell, FellReason: fellReason}
		if r.Encoder == "" || !attemptSteps(ctx, err, res.Errors, r.Full, hooks.Stopped, policyOf(in.Plan.Video)) {
			return res
		}
		reason := firstLines(res.Errors, 1)
		if reason == "" {
			reason = err.Error()
		}
		if hooks.Failed != nil {
			hooks.Failed(r, res.Errors, err)
		}
		// Truncate, don't remove: the staged file is checked by identity (staged.intact), so the retry must
		// write into the same file, not a new one that may get another inode.
		if terr := os.Truncate(in.Tmp, 0); terr != nil {
			res.Err = fmt.Errorf("clearing the partial output: %w", terr)
			return res
		}
		if r.Full {
			next := Rung{Encoder: r.Encoder}
			next.Args, _ = in.Plan.FFmpegArgsWith(in.SrcPath, in.Tmp, r.Encoder)
			r = next
			if hooks.Note != nil {
				hooks.Note(fmt.Sprintf("full GPU pipeline failed (%s); the processor decodes for %s", shortReason(reason), next.Encoder))
			}
		} else {
			fell, fellReason = true, shortReason(reason)
			r = Rung{Args: in.Plan.FFmpegArgs(in.SrcPath, in.Tmp)}
		}
		if hooks.OnRung != nil {
			hooks.OnRung(r)
		}
	}
}

// hardwarePolicy is the Convert step's graphics choice. only: the file is never converted on the processor (Hardware
// "only"). fail: a graphics failure is final, it never steps down (HardwareFail "fail").
type hardwarePolicy struct{ only, fail bool }

func policyOf(v rules.VideoEncode) hardwarePolicy {
	return hardwarePolicy{only: v.Hardware == "only", fail: v.HardwareFail == "fail"}
}

// attemptSteps says whether a graphics rung that ended with err should convert again on the next rung. Off the full
// GPU pipeline, only an encoder or ffmpeg failure (jobs.FallsBackToProcessor). On the full rung the graphics card
// also decodes, and a decode error there can be the card's, not the file's (a profile or chroma layout it cannot
// read), so any failure steps down except a stop (cancel, shutdown, the stall stop), a lost lease, a full disk, a
// missing source or an unwritable output, which the processor would hit too. The policy can only stop a step.
func attemptSteps(ctx context.Context, err error, errText string, full bool, stopped func() bool, hw hardwarePolicy) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, errLeaseLost) {
		return false
	}
	if stopped != nil && stopped() {
		return false
	}
	if hw.fail || (hw.only && !full) { // a hybrid rung under "only" never hands the file to the processor
		return false
	}
	lines := strings.Split(errText, "\n")
	if full {
		switch jobs.FailureCode(jobs.ReasonConvertFailed, lines) {
		case "no_space", "source_missing", "output_not_writable", "settings_rejected":
			return false
		}
		return true
	}
	return jobs.FallsBackToProcessor(lines)
}
