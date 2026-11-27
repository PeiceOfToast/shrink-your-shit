// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// End is what the worker does with a job once its run has returned.
type End int

const (
	// EndFinish writes the job's result with Finish. It counts a try only for a retry or a fail.
	EndFinish End = iota
	// EndRequeue puts the job back to waiting. It is never a try (L-Q-5).
	EndRequeue
	// EndCancel cancels the job. Its row may already be cancelled by a person; that is not an error.
	EndCancel
)

// settle maps a flow result to what the queue records. stopping is true when the worker is shutting down: a
// cancelled run then goes back to waiting instead of being cancelled, because the person did not ask for it.
func settle(res flow.Result, stopping bool) (jobs.Result, End) {
	switch res.Outcome {
	case flow.OutcomeSmaller, flow.OutcomeBiggerKept:
		return jobs.Result{
			Verdict: jobs.VerdictDone, Outcome: string(res.Outcome),
			Reason: res.Message, NextAction: res.NextAction,
			SizeBefore: res.SizeBefore, SizeAfter: res.SizeAfter, OutputPath: res.OutputPath,
		}, EndFinish
	case flow.OutcomeSkip:
		return jobs.Result{
			Verdict: jobs.VerdictSkip, SkipReason: reasonOr(res, "skip"), Reason: messageOr(res, "skipped"),
			FailedStep: res.FailedStep, NextAction: res.NextAction,
		}, EndFinish
	case flow.OutcomeMayPass:
		return jobs.Result{
			Verdict: jobs.VerdictRetry, Reason: sentenceOr(res, "run.may_pass"), FailedStep: res.FailedStep,
			NextAction: res.NextAction,
		}, EndFinish
	case flow.OutcomeFatal:
		return jobs.Result{
			Verdict: jobs.VerdictFail, Reason: sentenceOr(res, "run.fatal"), FailedStep: res.FailedStep,
			NextAction: res.NextAction,
		}, EndFinish
	case flow.OutcomeRequeue:
		return jobs.Result{}, EndRequeue
	case flow.OutcomeCancelled:
		if stopping {
			return jobs.Result{}, EndRequeue
		}
		return jobs.Result{}, EndCancel
	}
	// Run promises a non-empty outcome; an empty one is a bug in flow, so the job fails loudly.
	return jobs.Result{
		Verdict: jobs.VerdictFail, Reason: "internal_error", FailedStep: "worker",
		NextAction: "report_bug",
	}, EndFinish
}

// reasonOr returns the flow reason, or fallback when flow left it empty (Finish needs a reason).
func reasonOr(res flow.Result, fallback string) string {
	if res.Reason != "" {
		return res.Reason
	}
	return fallback
}

// sentenceOr is the sentence a failed or retried job shows the person: flow's message, else its code, else fallback.
func sentenceOr(res flow.Result, fallback string) string {
	if res.Message != "" {
		return res.Message
	}
	return reasonOr(res, fallback)
}

// messageOr returns the sentence the person reads, or fallback when flow left it empty.
func messageOr(res flow.Result, fallback string) string {
	if res.Message != "" {
		return res.Message
	}
	return fallback
}
