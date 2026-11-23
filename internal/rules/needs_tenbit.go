// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "github.com/shrink-your-shit/shrink-your-shit/internal/media"

// NeedsTenBit says whether a job's file needs a 10-bit encoder: an HDR plan, a plan that keeps a 10-bit source
// (VideoEncode.TenBit, set by Compile), or, for a plan from a server that did not set TenBit, a 10-bit source
// (source is the probe of the original; nil when unknown). A copied video or a plan with no codec needs none.
// This is the only place that answers the question: the local worker's encoder choice, its graphics decode probe
// and its "converting to" line, and the remote worker's encoder choice all read it.
func NeedsTenBit(p Plan, source *media.Info) bool {
	if p.Video.HDR != nil || p.Video.TenBit {
		return true
	}
	if p.Video.BitDepth == 8 { // the person forced 8 bits: a 10-bit source does not need a 10-bit encoder
		return false
	}
	if source == nil || p.Video.Codec == "" || p.Video.Copy {
		return false
	}
	v := source.Video()
	return v != nil && v.BitDepth > 8
}
