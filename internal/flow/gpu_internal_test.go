// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The rules the ladder reads, one test each: the failure codes in L-HW-14's order, the step-down
// table (L-HW-5), the wording (L-HW-7) and what a plan asks of a card (L-HW-9).

func TestFailureCodes(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		code  string
	}{
		{"a header the file type cannot hold", []string{"[out#0/mp4] Could not write header (incorrect codec parameters ?): Invalid argument",
			"Stream #0:1: Audio: opus, which is not supported for mp4"}, codeSettingsRejected},
		{"an option the encoder refused", []string{"[libopus @ 0x55] Quality-based encoding not supported"}, codeSettingsRejected},
		{"an unknown encoder", []string{"Unknown encoder 'hevc_qsv'"}, codeEncoderUnavailable},
		{"a session the card refused", []string{"[hevc_qsv @ 0x55] Error creating a MFX session: -9."}, codeEncoderUnavailable},
		{"no entrypoint on VA-API", []string{"[hevc_vaapi @ 0x55] No usable encoding entrypoint found"}, codeEncoderUnavailable},
		{"a full disk", []string{"av_interleaved_write_frame(): No space left on device"}, codeNoSpace},
		{"an unwritable output", []string{"[out#0/mkv] Error opening output out.mkv: Permission denied"}, codeNotWritable},
		{"a missing source", []string{"[in#0 @ 0x55] Error opening input: No such file or directory"}, codeSourceMissing},
		{"a damaged source", []string{"[h264 @ 0x55] Invalid data found when processing input"}, codeSourceDamaged},
		{"anything else", []string{"[libx265 @ 0x55] some new failure"}, codeFFmpeg},
		// The order is the rule: an unavailable encoder is checked before the disk, and an unknown
		// option name is the ladder's own failure (#609), not a refused setting.
		{"an unavailable encoder on a full disk", []string{"Unknown encoder 'hevc_qsv'", "No space left on device"}, codeEncoderUnavailable},
		{"an unknown option", []string{"[libopus @ 0x55] Option not found"}, codeFFmpeg},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failureCode(c.lines); got != c.code {
				t.Errorf("failureCode(%q) = %q, want %q", c.lines, got, c.code)
			}
		})
	}
}

// TestStepsDownRules is L-HW-5's table: the four failures the processor would hit too never step
// down, on the full rung any other failure does, and off it only an encoder or ffmpeg failure does.
func TestStepsDownRules(t *testing.T) {
	full := encode.Rung{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Decode: encode.DecodeHardware}
	hybrid := encode.Rung{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Decode: encode.DecodeSoftware}
	cases := []struct {
		code           string
		stepsOffFull   bool
		stepsOffHybrid bool
	}{
		{codeEncoderUnavailable, true, true},
		{codeFFmpeg, true, true},
		{codeSourceDamaged, true, false},
		{codeNoSpace, false, false},
		{codeSourceMissing, false, false},
		{codeNotWritable, false, false},
		{codeSettingsRejected, false, false},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			if got := stepsDown(full, c.code); got != c.stepsOffFull {
				t.Errorf("off the full rung on %s: stepsDown = %v, want %v", c.code, got, c.stepsOffFull)
			}
			if got := stepsDown(hybrid, c.code); got != c.stepsOffHybrid {
				t.Errorf("off the hybrid rung on %s: stepsDown = %v, want %v", c.code, got, c.stepsOffHybrid)
			}
		})
	}
}

// TestFallbackWording is L-HW-7: the friendly name and the encoder, the reason, and what happened
// instead; the reason is capped at 300 characters.
func TestFallbackWording(t *testing.T) {
	rung := encode.Rung{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Decode: encode.DecodeSoftware}
	if got, want := fallbackLine(rung, "Error creating a MFX session: -9."),
		"Intel Quick Sync (hevc_qsv) failed: Error creating a MFX session: -9. Converted on the processor instead."; got != want {
		t.Errorf("fallbackLine = %q, want %q", got, want)
	}
	if got, want := fallbackLine(encode.Rung{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI}, "no device"),
		"graphics card (hevc_vaapi) failed: no device. Converted on the processor instead."; got != want {
		t.Errorf("fallbackLine = %q, want %q", got, want)
	}
	long := strings.Repeat("x", reasonMax+100)
	line := fallbackLine(rung, long)
	if !strings.Contains(line, strings.Repeat("x", reasonMax)) || strings.Contains(line, strings.Repeat("x", reasonMax+1)) {
		t.Errorf("fallbackLine did not cap the reason at %d characters: %d x's", reasonMax, strings.Count(line, "x"))
	}
	// The stop wording names the same failure and says the file was left alone (L-HW-5).
	if got := stopLine(rung, "no device"); !strings.HasPrefix(got, "Intel Quick Sync (hevc_qsv) failed: no device.") ||
		!strings.Contains(got, "left as it is") {
		t.Errorf("stopLine = %q, want the card, the reason and the untouched original", got)
	}
}

// TestWantForAsksForWhatThePlanNeeds is L-HW-9's side of the picker: the codec, ten bits, HDR
// mastering data, a scale, and whether the full pipeline is possible.
func TestWantForAsksForWhatThePlanNeeds(t *testing.T) {
	sdr8 := media.Info{Video: []media.VideoTrack{{Index: 0, Codec: "h264", Width: 3840, Height: 2160, BitDepth: 8}}}
	ten := media.Info{Video: []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, BitDepth: 10}}}
	hdr := media.Info{Video: []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, BitDepth: 10,
		ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		HDR: media.HDR{MasteringDisplay: true, ContentLight: true}}}}

	cases := []struct {
		name string
		plan encode.Plan
		info media.Info
		want Want
	}{
		{"eight-bit SDR", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}, sdr8,
			Want{Codec: encode.CodecHEVC, FullOK: true}},
		{"a scale", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, Scale: encode.Scale{Height: 1080}}}, sdr8,
			Want{Codec: encode.CodecHEVC, Scales: true, FullOK: true}},
		{"ten-bit SDR", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}, ten,
			Want{Codec: encode.CodecHEVC, TenBit: true, FullOK: true}},
		{"HDR kept with mastering data", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, HDR: encode.HDRKeep}}, hdr,
			Want{Codec: encode.CodecHEVC, TenBit: true, HDR: true, FullOK: true}},
		{"HDR tone-mapped", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, HDR: encode.HDRTonemap}}, hdr,
			Want{Codec: encode.CodecHEVC}},
		{"a frame-rate change", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, FPS: 30}}, sdr8,
			Want{Codec: encode.CodecHEVC}},
		{"a bit-depth change", encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, BitDepth: encode.Depth8}}, ten,
			Want{Codec: encode.CodecHEVC}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantFor(c.plan, c.info); got != c.want {
				t.Errorf("wantFor = %+v, want %+v", got, c.want)
			}
		})
	}

	// HLG has no mastering data, so any ten-bit card may write it.
	hlg := media.Info{Video: []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, BitDepth: 10,
		ColorPrimaries: "bt2020", ColorTransfer: "arib-std-b67", ColorSpace: "bt2020nc"}}}
	if got := wantFor(encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, HDR: encode.HDRKeep}}, hlg); got.HDR {
		t.Errorf("wantFor(HLG) = %+v, want HDR false: there is no mastering data to keep", got)
	}
}

// TestGpuChoiceReadsTheVideoStep is page 07's pair: a card is used only when the video step says
// so, and ifGpuFails decides what a card failure may fall back to.
func TestGpuChoiceReadsTheVideoStep(t *testing.T) {
	decide := func(detail F) Decisions {
		return Decisions{Steps: []StepDecision{{ID: "video", Decision: Decision{Work: true, Detail: detail}}}}
	}
	cases := []struct {
		name  string
		d     Decisions
		use   bool
		fails string
	}{
		{"the graphics card is on", decide(F{"useGpu": true, "ifGpuFails": "cpu"}), true, "cpu"},
		{"stop instead", decide(F{"useGpu": true, "ifGpuFails": "stop"}), true, "stop"},
		{"the graphics card is off", decide(F{"useGpu": false, "ifGpuFails": "cpu"}), false, "cpu"},
		{"an older detail falls back to the processor", decide(F{"format": "hevc"}), false, "cpu"},
		{"no video work", Decisions{Steps: []StepDecision{{ID: "video", Decision: Decision{Detail: F{"useGpu": true}}}}}, false, "cpu"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			use, fails := gpuChoice(c.d)
			if use != c.use || fails != c.fails {
				t.Errorf("gpuChoice = (%v, %q), want (%v, %q)", use, fails, c.use, c.fails)
			}
		})
	}
}

// TestPickFromAnswerBridgesThePlansNeeds checks that a job's Want reaches hw.Pick: the device
// records gate the rungs a job is offered (L-HW-9, L-HW-11).
func TestPickFromAnswerBridgesThePlansNeeds(t *testing.T) {
	ans := hw.Answer{Devices: []hw.DeviceRecord{{
		Candidate: hw.Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"},
		Decode:    true, Scale: true, Encode10: true, HDRSurvives: true,
	}}}
	got := PickFromAnswer(ans, Want{Codec: encode.CodecHEVC, FullOK: true})
	if len(got.Rungs) != 2 || got.Rungs[0].Decode != encode.DecodeHardware || got.Rungs[1].Decode != encode.DecodeSoftware {
		t.Errorf("PickFromAnswer = %+v, want the full pipeline then the hybrid", got)
	}
	refused := PickFromAnswer(ans, Want{Codec: encode.CodecHEVC, TenBit: false, FullOK: false})
	if len(refused.Rungs) != 1 || refused.Rungs[0].Decode != encode.DecodeSoftware {
		t.Errorf("PickFromAnswer(no full) = %+v, want the hybrid rung only", refused)
	}
	none := PickFromAnswer(hw.Answer{}, Want{Codec: encode.CodecHEVC})
	if len(none.Rungs) != 0 || none.Reason == "" {
		t.Errorf("PickFromAnswer(no card) = %+v, want no rungs and a reason", none)
	}
}
