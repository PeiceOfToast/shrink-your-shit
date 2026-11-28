// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// A retry is offered for every failed or cancelled job except where running the file again would do the
// wrong thing: the three replace failures leave the original in the trash or the new file's record
// unsettled, so the file needs a look before it can run again (build-order stage 3 item 6, #865). A stall
// is never retried automatically (L-Q-3, L-Q-6), but the person may still press Retry after reading the
// log (lead, 11-28). The row keeps its failure's code for this (S3-6a); a job that failed before the code
// was recorded has none and stays retryable.
var retryRefused = map[string]string{
	"replace.record_unsettled":  "The new file is there but its record is not settled. Check the file names; the next time Shrink Your Shit starts it settles them.",
	"replace.put_back_failed":   "The original is in the trash. Put it back before running this file again.",
	"replace.stranger_in_trash": "Something else is in the trash folder. Check it before running this file again.",
}

// RetryRefused says whether a failure's code makes Retry the wrong action, and the sentence that says what
// to do instead. A job in the wrong state is not this: its state says so on its own.
func RetryRefused(failureCode string) (message string, refused bool) {
	message, refused = retryRefused[failureCode]
	return message, refused
}

// ErrNotRetryable is Queue.Retry refusing because the failure needs a look, not another run. Message is the
// sentence the person reads.
type ErrNotRetryable struct{ Message string }

func (e *ErrNotRetryable) Error() string { return e.Message }
