// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"slices"
	"strings"
)

// HDRCarrier says whether the graphics encoder enc is proven to keep HDR10's static metadata: its profile says so
// (carriesHDR). An encoder with no profile is not.
func HDRCarrier(enc string) bool {
	p, _ := ProfileFor(enc)
	return p.carriesHDR
}

// PassedGraphics is the graphics encoders whose capability test passed on a worker, in the doctor's order. caps
// is the worker's capability list (its own, or the one it reported).
func PassedGraphics(caps []map[string]any) []string {
	out := []string{}
	for _, c := range caps {
		key, _ := c["key"].(string)
		if c["kind"] == "encoder" && c["slot"] == "gpu" && c["test"] == "passed" {
			out = append(out, strings.TrimPrefix(key, "encoder:"))
		}
	}
	return out
}

// passedTenBit is the graphics encoders that passed the 10-bit test as well (HDR needs that).
func passedTenBit(caps []map[string]any) []string {
	out := []string{}
	for _, e := range PassedGraphics(caps) {
		for _, c := range caps {
			if key, _ := c["key"].(string); key == "encoder:"+e && c["tenBit"] == true {
				out = append(out, e)
			}
		}
	}
	return out
}

// EncoderFor is the graphics encoder a worker converts a codec's plan with: main's encoder for the codec's
// format, when the worker's own test passed it (passed lists those). A file that is 10-bit (need10: an HDR
// plan, or a source whose video is 10-bit, SDR or not) also needs that encoder's 10-bit test to have passed,
// and an HDR plan whose source has mastering data needs a family proven to keep it. "" with a note for the
// job log when the processor is used because of a test or a family; "" without one for the processor by
// choice (main cpu, or no graphics encoder for the format).
func EncoderFor(codec, main string, need10 bool, hdr *HDR, caps []map[string]any) (enc, note string) {
	if main == "" || main == "cpu" {
		return "", ""
	}
	want := GraphicsEncoder(codec, main)
	if want == "" {
		return "", ""
	}
	if !slices.Contains(PassedGraphics(caps), want) {
		return "", fmt.Sprintf("encoder %s didn't pass its test here; using the processor", want)
	}
	if hdr == nil && !need10 {
		return want, ""
	}
	if !slices.Contains(passedTenBit(caps), want) {
		if hdr != nil {
			return "", fmt.Sprintf("HDR needs a 10-bit encoder: %s didn't pass its 10-bit test here; using the processor", want)
		}
		// An SDR file can be 10-bit too (a 10-bit source kept at its depth): the same test decides it, so
		// an 8-bit-only encoder is not used and then refused by the output check after a wasted encode.
		return "", fmt.Sprintf("this file is 10-bit: %s didn't pass its 10-bit test here; using the processor", want)
	}
	if hdr == nil {
		return want, ""
	}
	if (hdr.MasterDisplay != "" || hdr.CLL != "") && !HDRCarrier(want) {
		return "", fmt.Sprintf("HDR10 with mastering data: using the processor, %s isn't on the list of encoders proven to keep that data", want)
	}
	return want, ""
}
