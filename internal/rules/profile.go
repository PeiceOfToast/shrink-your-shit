// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "strconv"

// EncoderProfile is what one encoder family needs to be built: its quality options, the pixel format its 10-bit
// frames are sent as, its hardware set-up, its scale and its upload. The processor has a profile too, with none of
// those set: its quality and pixel format come from the codec. Families move into profiles one at a time (foundation
// slice 5); a family without one here is still built by the switches in plan.go.
type EncoderProfile struct {
	Family string
	// quality is the family's constant-quality arguments for a CRF; nil when the profile does not set it.
	quality func(crf int) []string
	// PixFmt10 is the pixel format 10-bit frames are sent as ("" when the profile does not set one: the family's
	// upload makes them instead).
	PixFmt10 string
	// setup are the hardware arguments placed before the input (the device is set up there); nil when none.
	setup func(device string) []string
	// fullDecode are the arguments that decode on the family's hardware for the full pipeline; nil when the family
	// has no full pipeline.
	fullDecode []string
	// scale is the family's scale filter for a height on the full pipeline; nil when the family does not set one.
	scale func(h int, tenBit bool) string
	// upload is the hybrid path's filter chain: software frames go to the device last, after any scale, in the pixel
	// format the source's depth needs. nil when the family takes software frames as they are.
	upload func(tenBit bool) []string
	// carriesHDR is whether the family's output keeps an HDR10 source's static metadata (mastering display and light
	// level). Checked on CT 117 with ffmpeg 6.1.1: QSV and VAAPI kept both. NVENC and AMF are not proven.
	carriesHDR bool
	// unavailable are the phrases in ffmpeg's output that say this family's encoder can't be used here.
	unavailable []string
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

// Upload is the family's hybrid upload filter chain for frames of the given depth: nil when it has none.
func (p EncoderProfile) Upload(tenBit bool) []string {
	if p.upload == nil {
		return nil
	}
	return p.upload(tenBit)
}

// vaapiFormat is the pixel format VAAPI's frames are made in: p010 for a 10-bit source, nv12 otherwise.
func vaapiFormat(tenBit bool) string {
	if tenBit {
		return "p010"
	}
	return "nv12"
}

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
		carriesHDR:  true,
		unavailable: []string{"error creating a mfx session"},
	},
	"vaapi": {
		Family: "vaapi",
		quality: func(crf int) []string {
			return []string{"-rc_mode", "CQP", "-qp", strconv.Itoa(crf)}
		},
		setup: func(device string) []string {
			return []string{"-init_hw_device", "vaapi=va:" + device, "-filter_hw_device", "va"}
		},
		fullDecode: []string{"-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-hwaccel_device", "va"},
		// scale_vaapi sets the frames' format too (nv12, or p010 for 10-bit). Its width is -2, which ffmpeg rounds to
		// an even width; that is not yet shown to equal ScaledWidth's (see the PR).
		scale: func(h int, tenBit bool) string {
			return "scale_vaapi=w=-2:h=" + strconv.Itoa(h) + ":format=" + vaapiFormat(tenBit)
		},
		upload: func(tenBit bool) []string {
			return []string{"format=" + vaapiFormat(tenBit), "hwupload"}
		},
		carriesHDR: true,
	},
	"amf": {
		Family: "amf",
		quality: func(crf int) []string {
			return []string{"-rc", "cqp", "-qp_i", strconv.Itoa(crf), "-qp_p", strconv.Itoa(crf)}
		},
		PixFmt10: "p010le",
	},
}

// UnavailablePhrases are the "encoder unavailable" phrases of every family's profile, in the families' order. The
// failure classifier adds them to its generic phrases.
func UnavailablePhrases() []string {
	var out []string
	for _, fam := range graphicsFamilies {
		out = append(out, profiles[fam].unavailable...)
	}
	return out
}

// ProfileFor is the profile of enc: "" or "cpu" for the processor, or a graphics encoder's name (hevc_nvenc). ok is
// false for an encoder with no profile, such as an unknown encoder like hevc_videotoolbox.
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
