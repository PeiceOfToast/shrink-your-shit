// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "github.com/PeiceOfToast/shrink-your-shit/internal/hw"

// PickFromAnswer is the worker's picker: it asks hw.Pick for the rungs a finished detection pass
// allows, so a card rung is offered only for the tests the card passed — its codec's test encode,
// its ten-bit encode when the output is ten-bit, its HDR probe when mastering data must survive,
// and its decode and scale for the full pipeline (L-HW-9, L-HW-11). The processor rung is not this
// function's to add: whether a card failure may fall back to it is the step's ifGpuFails.
func PickFromAnswer(ans hw.Answer, want Want) Attempt {
	got := hw.Pick(ans.Inventory, ans.Devices, hw.Need{
		Codec:  want.Codec,
		TenBit: want.TenBit,
		HDR:    want.HDR,
		Scales: want.Scales,
		FullOK: want.FullOK,
	})
	return Attempt{Rungs: got.Rungs, Line: got.Line, Reason: got.Reason}
}
