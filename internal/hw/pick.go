// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
)

// Need is what one job asks of a card (L-HW-9): the codec it writes, whether the output is ten-bit,
// whether an HDR picture's mastering data must survive the encode, whether the plan scales, and
// whether the full pipeline is possible at all.
type Need struct {
	Codec  encode.Codec
	TenBit bool
	// HDR is true when the source carries mastering data the output must keep.
	HDR    bool
	Scales bool
	// FullOK is true when nothing in the plan needs the processor's frames: no frame-rate change, no
	// tone map, and no bit-depth change. Whether the full pipeline is offered also waits on the card
	// probe's decode and scale (L-HW-11).
	FullOK bool
	// Bitrate is true when the plan carries a set bitrate. The card families' bitrate flags are not
	// part of this stage (L-HW-10 is quality-only), so no card can take such a job.
	Bitrate bool
}

// Attempt is the rungs to try for one job, in order (L-HW-5), with the line the job log shows for the
// first (L-HW-7, L-HW-9). When no card can do the job, Rungs is empty and Reason says why.
type Attempt struct {
	Rungs  []encode.Rung
	Line   string
	Reason string
}

// ProductName is the friendly name L-HW-7 gives a card family in the job log.
func ProductName(f encode.Family) string {
	switch f {
	case encode.FamilyQSV:
		return "Intel Quick Sync"
	case encode.FamilyNVENC:
		return "NVIDIA"
	case encode.FamilyAMF:
		return "AMD"
	case encode.FamilyVAAPI:
		return "graphics card"
	case encode.FamilyCPU:
		return "processor"
	}
	return string(f)
}

// Pick chooses the rungs for need from a finished pass: the inventory, and the device records the
// probe wrote for the encoders it found (L-HW-9). A card rung is offered only for a test the card
// passed: its codec's test encode, its ten-bit encode when the output is ten-bit, its HDR probe when
// the source's mastering data must survive, and, for the full pipeline, a decode and scale that both
// passed (L-HW-11). A job with a set bitrate is never offered a card rung: that is what the processor
// is for until the card families' bitrate flags arrive (L-HW-10).
func Pick(inv Inventory, devices []DeviceRecord, need Need) Attempt {
	if need.Bitrate {
		// L-HW-10 is quality-only for now: the card encoders cannot follow a set bitrate, and the
		// processor honours the number the person chose.
		return Attempt{Reason: "a set bitrate isn't built for the graphics card yet"}
	}
	var failed []string
	for _, d := range devices {
		if d.Codec != string(need.Codec) {
			continue
		}
		name := ProductName(d.Family) + " (" + d.Encoder + ")"
		if need.TenBit && !d.Encode10 {
			failed = append(failed, name+" did not pass the 10-bit test")
			continue
		}
		if need.HDR && !d.HDRSurvives {
			failed = append(failed, name+" did not keep HDR")
			continue
		}
		if need.Scales && !d.Scale && d.Family == encode.FamilyQSV {
			// A QSV hybrid rung scales on the card; NVENC's and VAAPI's hybrid rungs scale on the
			// processor, so their probe's scale step is not in their way here.
			failed = append(failed, name+" could not scale")
			continue
		}
		card := encode.Rung{Encoder: d.Encoder, Family: d.Family, Decode: encode.DecodeSoftware, Device: d.Device}
		rungs := []encode.Rung{card}
		full := false
		if need.FullOK && d.Decode && d.Scale && (d.Family == encode.FamilyQSV || d.Family == encode.FamilyVAAPI) {
			card.Decode = encode.DecodeHardware
			rungs = []encode.Rung{card, {Encoder: d.Encoder, Family: d.Family, Decode: encode.DecodeSoftware, Device: d.Device}}
			full = true
		}
		line := "Encoding on " + name
		if full {
			line += "; the card decodes the picture too."
		} else {
			line += "; the processor decodes the picture."
		}
		return Attempt{Rungs: rungs, Line: line}
	}
	if len(failed) > 0 {
		return Attempt{Reason: joinReasons(failed)}
	}
	if r := firstReason(inv, string(need.Codec)); r != "" {
		return Attempt{Reason: r}
	}
	return Attempt{Reason: "no graphics card was found"}
}

// joinReasons joins the reasons the cards could not do the job into one sentence fragment. A single
// card's reason is used as it is; several are joined with "and".
func joinReasons(reasons []string) string {
	switch len(reasons) {
	case 1:
		return reasons[0]
	case 2:
		return reasons[0] + " and " + reasons[1]
	}
	out := ""
	for i, r := range reasons {
		switch {
		case i == 0:
			out = r
		case i == len(reasons)-1:
			out += ", and " + r
		default:
			out += ", " + r
		}
	}
	return out
}

// firstReason is the plain reason a codec has no card, from the pass's verdicts: the first candidate
// on a family that proved another encoder (the card this machine really has), else the first one.
func firstReason(inv Inventory, codec string) string {
	proven := map[encode.Family]bool{}
	for _, v := range inv.Verdicts {
		if v.Available {
			proven[v.Family] = true
		}
	}
	first := ""
	for _, v := range inv.Verdicts {
		if v.Codec != codec || v.Available || v.Reason == "" {
			continue
		}
		if proven[v.Family] {
			return v.Reason
		}
		if first == "" {
			first = v.Reason
		}
	}
	return first
}
