// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"slices"
	"strings"
)

// hdrCarriers are the graphics families whose output keeps an HDR10 source's static metadata (mastering display
// and light level) when the frames are encoded from the source. Checked on CT 117 with ffmpeg 6.1.1: QSV and
// VAAPI kept both. nvenc and amf are not proven, so an HDR10 file with that metadata goes to the processor there.
var hdrCarriers = map[string]bool{"qsv": true, "vaapi": true}

// HDRCarrier says whether the graphics encoder enc is proven to keep HDR10's static metadata (hdrCarriers).
func HDRCarrier(enc string) bool { return hdrCarriers[GraphicsFamily(enc)] }

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
// format, when the worker's own test passed it. An HDR plan (hdr non-nil) also needs that encoder's 10-bit test
// to have passed, and a family proven to keep the source's mastering data when the source has any. "" with a
// note for the job log when the processor is used because of a test or a family; "" without one for the
// processor by choice (main cpu, or no graphics encoder for the format).
func EncoderFor(codec, main string, hdr *HDR, caps []map[string]any) (enc, note string) {
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
	if hdr == nil {
		return want, ""
	}
	if !slices.Contains(passedTenBit(caps), want) {
		return "", fmt.Sprintf("HDR needs a 10-bit encoder: %s didn't pass its 10-bit test here; using the processor", want)
	}
	if (hdr.MasterDisplay != "" || hdr.CLL != "") && !HDRCarrier(want) {
		return "", fmt.Sprintf("HDR10 with mastering data: using the processor, %s isn't proven to keep that data here", want)
	}
	return want, ""
}
