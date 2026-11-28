// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// replaceKeepDays is how long a replaced original stays in the trash. The page promises 7 days; the daily sweep that
// removes it is S2-10c, so until then the copies stay.
const replaceKeepDays = 7

// Replacer swaps a job's original for its new file, journalled (S2-10b). The worker's environment has the real one;
// a try never replaces, so its environment has none.
type Replacer interface {
	Swap(ctx context.Context, p safety.SwapPlan) (safety.SwapResult, error)
}

// RealReplacer is the swap with the replace journal in the store.
type RealReplacer struct{ Journal *safety.Journal }

// Swap runs safety.Swap against the journal.
func (r RealReplacer) Swap(ctx context.Context, p safety.SwapPlan) (safety.SwapResult, error) {
	return safety.Swap(ctx, r.Journal, p)
}

// saveReplaces reports whether the flow's save step replaces the original rather than writing next to it.
func saveReplaces(f Flow) bool {
	for _, ref := range f.Steps {
		if ref.Type != "file.save" {
			continue
		}
		var s struct {
			Where string `json:"where"`
		}
		return json.Unmarshal(ref.Settings, &s) == nil && s.Where == "replace"
	}
	return false
}

// replaceOriginal swaps the original for the staged file. Each refusal is the outcome the brief asks for: a trash
// folder or read-only library, a changed or shared original, and a put-back that failed. A kept original is not a
// failed job, except when the put-back failed (then the original sits in the trash and a person must look).
//
// The subtitle files went beside the new file before this (placeSidecars). A swap that settles keeps them under the
// job's names; a refusal removes them, as the place does. A cancel leaves their records for the next start to settle.
func (e *engine) replaceOriginal(ctx context.Context, staged safety.Staged, final string, after int64, placed *bool, sidecars []safety.Sidecar) Result {
	if e.env.Replacer == nil {
		e.removeSidecars(sidecars)
		return fatal("replace.unavailable", "This worker has no replace journal, so the original was kept.", "save", "")
	}
	_, err := e.env.Replacer.Swap(ctx, safety.SwapPlan{
		JobID: e.in.JobID, Original: e.in.Source, Staged: staged.Path, Final: final, KeepDays: replaceKeepDays,
	})
	// The swap happened but its record did not settle: the new file is in place, so its subtitle files belong with it
	// and are kept, the same as a settled swap (review #865, B3).
	if errors.Is(err, safety.ErrRecordNotSettled) {
		*placed = true
		e.finishSidecars()
		return fatal("replace.record_unsettled", "The new file is in place and the original is in the library's trash folder, but the replace record could not be saved. The next start settles the record; check the file names.", "save", "The next start settles the record; check the file names.")
	}
	if err == nil {
		*placed = true
		e.finishSidecars()
		_ = os.Remove(staged.Folder) // the staged file has left it; the folder goes too
		e.probeSide(ctx, "after", final)
		return Result{
			Outcome: OutcomeSmaller, SizeBefore: e.in.Size, SizeAfter: after, OutputPath: final,
			Message: "Replaced " + filepath.Base(final) + "; the original is in the trash.",
		}
	}
	if ctx.Err() == nil {
		e.removeSidecars(sidecars)
	}
	switch {
	case ctx.Err() != nil:
		return cancelled()
	case errors.Is(err, safety.ErrNoTrash), errors.Is(err, safety.ErrReadOnlyLibrary):
		return Result{Outcome: OutcomeMayPass, Reason: "replace.not_possible", Message: err.Error(),
			SizeBefore: e.in.Size, NextAction: "Make the library folder writable so a trash folder can be made there, or save the new file next to the original instead."}
	case errors.Is(err, safety.ErrSourceChanged):
		return Result{Outcome: OutcomeMayPass, Reason: "replace.source_changed",
			Message:    "The original changed while it was being replaced, so it was kept.",
			SizeBefore: e.in.Size, NextAction: "Try it again once nothing else is changing the file."}
	case errors.Is(err, safety.ErrSharedSource):
		return Result{Outcome: OutcomeSkip, Reason: "replace.shared",
			Message: "The original is hard-linked, so it is shared and was not replaced.", SizeBefore: e.in.Size}
	case errors.Is(err, safety.ErrPutBackFailed):
		return fatal("replace.put_back_failed", "The new file could not be placed, and the original could not be put back from the trash. The original is in the library's trash folder. Check that folder before retrying.", "save", "Check the library's trash folder and put the original back by hand.")
	case errors.Is(err, safety.ErrStrangerInTrash):
		return fatal("replace.stranger_in_trash", "A file that was not the original took the name and was moved to the trash folder, and it could not be put back. The name was not replaced; the file is in the library's trash folder. Check that folder before retrying.", "save", "Check the library's trash folder.")
	}
	return fatal("replace.failed", err.Error(), "save", "")
}
