// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "strconv"

// EncoderProfile is what one encoder family needs to be built: its quality options and the pixel format its
// 10-bit frames are sent as. The processor has a profile too, with neither set: its quality and pixel format come
// from the codec. Families move into profiles one at a time (foundation slice 5); a family without one here is
// still built by the switches in plan.go.
type EncoderProfile struct {
	Family string
	// quality is the family's constant-quality arguments for a CRF; nil when the profile does not set it.
	quality func(crf int) []string
	// PixFmt10 is the pixel format 10-bit frames are sent as ("" when the profile does not set one).
	PixFmt10 string
	// setup are the hardware arguments placed before the input (the device is set up there); nil when none.
	setup func(device string) []string
	// fullDecode are the arguments that decode on the family's hardware for the full pipeline; nil when the family
	// has no full pipeline.
	fullDecode []string
	// scale is the family's scale filter for a height on the full pipeline; nil when the family does not set one.
	scale func(h int, tenBit bool) string
}

// Setup is the family's hardware set-up for the input, given the render device (VAAPI's; QSV ignores it).
func (p EncoderProfile) Setup(device string) []string {
	if p.setup == nil {
		return nil
	}
	return p.setup(device)
}

// FullDecode is the family's decode arguments for the full graphics pipeline: nil when it has none.
func (p EncoderProfile) FullDecode() []string { return p.fullDecode }

// profiles are the families that have a profile, by family ("" is the processor).
var profiles = map[string]EncoderProfile{
	"": {Family: ""},
	"nvenc": {
		Family:   "nvenc",
		quality:  func(crf int) []string { return []string{"-rc", "vbr", "-cq", strconv.Itoa(crf)} },
		PixFmt10: "p010le",
	},
	"qsv": {
		Family:     "qsv",
		quality:    func(crf int) []string { return []string{"-global_quality", strconv.Itoa(crf)} },
		PixFmt10:   "p010le",
		setup:      func(string) []string { return []string{"-init_hw_device", "qsv=qsv:hw"} },
		fullDecode: []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-hwaccel_device", "qsv"},
		// scale_qsv refuses w=-2, so the width is an expression: trunc(H*iw/ih/2+0.5)*2 keeps the aspect ratio, rounds
		// half up and is even, the same width ScaledWidth plans (see gpuScale).
		scale: func(h int, _ bool) string {
			hs := strconv.Itoa(h)
			return "scale_qsv=w=trunc(" + hs + "*iw/ih/2+0.5)*2:h=" + hs
		},
	},
	"amf": {
		Family: "amf",
		quality: func(crf int) []string {
			return []string{"-rc", "cqp", "-qp_i", strconv.Itoa(crf), "-qp_p", strconv.Itoa(crf)}
		},
		PixFmt10: "p010le",
	},
}

// ProfileFor is the profile of enc: "" or "cpu" for the processor, or a graphics encoder's name (hevc_nvenc). ok is
// false for an encoder with no profile, which includes a family the profiles do not have yet (vaapi) and
// an unknown encoder such as hevc_videotoolbox.
func ProfileFor(enc string) (EncoderProfile, bool) {
	if enc == "cpu" {
		enc = ""
	}
	if enc != "" && !graphicsEncoders[enc] {
		return EncoderProfile{}, false
	}
	p, ok := profiles[GraphicsFamily(enc)]
	if enc == "" {
		p, ok = profiles[""]
	}
	return p, ok
}

// pixFmt10 is the pixel format a family's 10-bit frames are sent as: its profile's, or p010le for a family whose
// profile does not set one yet.
func pixFmt10(fam string) string {
	if p, ok := profiles[fam]; ok && p.PixFmt10 != "" {
		return p.PixFmt10
	}
	return "p010le"
}
