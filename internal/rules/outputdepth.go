// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "github.com/shrink-your-shit/shrink-your-shit/internal/media"

// OutputDepth is the colour depth of the plan's main video in the new file, 8 or 10 bits. gpu is the
// graphics encoder that runs, or "" for the processor. A card makes 10 bits when its arguments do
// (EncodeTenBit: HDR, or a 10-bit source on a target whose card encoder has one); the processor keeps the
// job's depth on every target (NeedsTenBit, the one rule for that question). The live "converting to" line
// and the new file's check both read it, so they cannot disagree about the file's depth (foundation review
// cause 1, slice 3).
func OutputDepth(p Plan, source *media.Info, gpu string) int {
	if gpu != "" {
		if p.EncodeTenBit() {
			return 10
		}
		return 8
	}
	if NeedsTenBit(p, source) {
		return 10
	}
	return 8
}
