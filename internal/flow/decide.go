// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "github.com/PeiceOfToast/shrink-your-shit/internal/media"

// DecideFile is the library's decision for one probed file under a published revision: queue it, or the reason it stays
// where it is. The scanner, the queue that a new workflow starts and the library preview all ask this, so they agree.
func DecideFile(rev Revision, info media.Info) (bool, string) {
	decisions, _, err := PlanFile(rev, info, Env{})
	if err != nil {
		return false, "plan_failed"
	}
	if !decisions.Work() {
		return false, "nothing_to_do"
	}
	return true, ""
}
