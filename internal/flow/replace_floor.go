// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
)

// ReplaceKeepFloor is the fewest days an original is kept while a workflow replaces originals (K17). A replace that
// keeps originals for less would let a purge remove them the next night, before the person has looked.
const ReplaceKeepFloor = 7

// KeepDaysKey is the settings key for processing.keepOriginalsDays.
const KeepDaysKey = "processing.keepOriginalsDays"

// UsesReplace reports whether a flow's save step replaces the original.
func UsesReplace(f Flow) bool { return saveReplaces(f) }

// ReplaceFloorProblem is the problem a flow that replaces originals has while keepOriginalsDays is below the floor,
// or nil when the flow is fine. The problem sits on the save step's where field.
func (s *Store) replaceFloorProblem(ctx context.Context, f Flow) *Problem {
	if !UsesReplace(f) {
		return nil
	}
	keep := settings.Get(ctx, s.db, KeepDaysKey, ReplaceKeepFloor)
	if keep >= ReplaceKeepFloor {
		return nil
	}
	return &Problem{StepID: "save", Field: "where", Code: "keep_too_few_days",
		Message: "Keep originals for at least 7 days in Settings before replacing them."}
}

// ReplaceInUse reports whether any live workflow's published revision replaces originals. The settings check uses it
// to refuse a keep below the floor (K17, rule 1).
func (s *Store) ReplaceInUse(ctx context.Context) (bool, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT r.flow FROM workflows w
		JOIN workflow_revisions r ON r.workflow_id = w.id AND r.rev = w.current_rev
		WHERE w.deleted_at IS NULL AND w.current_rev IS NOT NULL`)
	if err != nil {
		return false, fmt.Errorf("read the published workflows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return false, err
		}
		var f Flow
		if json.Unmarshal([]byte(text), &f) == nil && UsesReplace(f) {
			return true, nil
		}
	}
	return false, rows.Err()
}
