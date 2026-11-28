// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
)

// The raw texts are what ffmpeg printed on the proof cards (slices/15-rebuild/s3-1b-hw-proofs.md), each with the card's
// family and codec. Each must read as the plain sentence the user needs.
func TestPlainReasonForTheProofCards(t *testing.T) {
	cases := []struct {
		name string
		c    Candidate
		raw  string
		want string
		// works is true for a card that proved another encoder: only that reading makes
		// "Conversion failed!" mean the card has no AV1 encoder (build-order item 3).
		works bool
	}{
		{"Debian 13 Intel QSV has no runtime",
			Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"},
			"run ffmpeg: ffmpeg exited with code 171: Error parsing global options: Unknown error occurred",
			"no QSV runtime for this chip on this system", false},
		{"QSV with a generic library error",
			Candidate{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"},
			"run ffmpeg: ffmpeg exited with code 187: Error parsing global options: Generic error in an external library",
			"no QSV runtime for this chip on this system", false},
		{"NVIDIA AV1 has no encoder on the card",
			Candidate{Encoder: "av1_nvenc", Family: encode.FamilyNVENC, Codec: "av1"},
			"run ffmpeg: ffmpeg exited with code 255: Conversion failed!",
			"this card has no AV1 encoder", true},
		{"Intel gen9 VAAPI AV1 has no encoder on the card",
			Candidate{Encoder: "av1_vaapi", Family: encode.FamilyVAAPI, Codec: "av1", Device: "/dev/dri/renderD128"},
			"run ffmpeg: ffmpeg exited with code 218: Conversion failed!",
			"this card has no AV1 encoder", true},
		{"the driver has no HEVC encode entrypoint",
			Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"},
			"run ffmpeg: ffmpeg exited with code 218: [hevc_vaapi @ 0x1] No usable encoding entrypoint found for profile VAProfileHEVCMain (17).",
			"this card's VA-API driver has no HEVC encoder", false},
		{"the static ffmpeg's libva is too old",
			Candidate{Encoder: "h264_vaapi", Family: encode.FamilyVAAPI, Codec: "h264", Device: "/dev/dri/renderD128"},
			"run ffmpeg: ffmpeg exited with code -1: ffmpeg: libva.so.2.init.c:290: _libva_so_2_tramp_resolve: Assertion `0 && \"Assertion in generated code\"' failed.",
			"the VA-API library on this system is too old for this ffmpeg", false},
		{"the libva-drm assertion reads the same way",
			Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"},
			"run ffmpeg: ffmpeg exited with code -1: ffmpeg: libva-drm.so.2.init.c:122: load_library: Assertion `0 && \"Assertion in generated code\"' failed.",
			"the VA-API library on this system is too old for this ffmpeg", false},
		{"an unknown message keeps its own words",
			Candidate{Encoder: "hevc_nvenc", Family: encode.FamilyNVENC, Codec: "hevc"},
			"run ffmpeg: ffmpeg exited with code 1: something new",
			"the driver refused: run ffmpeg: ffmpeg exited with code 1: something new", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := plainReasonOn(tc.c, errString(tc.raw), tc.works); got != tc.want {
				t.Errorf("plainReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// "Conversion failed!" is only read as a missing AV1 encoder on a card that proved another encoder;
// on a card that proved nothing it keeps the driver's words (build-order item 3).
func TestConversionFailedOnlyForACardThatWorks(t *testing.T) {
	c := Candidate{Encoder: "av1_nvenc", Family: encode.FamilyNVENC, Codec: "av1"}
	raw := errString("run ffmpeg: ffmpeg exited with code 255: Conversion failed!")
	if got := plainReasonOn(c, raw, false); strings.Contains(got, "AV1") {
		t.Errorf("a card that proved nothing got the AV1 sentence: %q", got)
	}
	if got := plainReasonOn(c, raw, true); got != "this card has no AV1 encoder" {
		t.Errorf("a working card = %q, want the AV1 sentence", got)
	}
}

// The decode step keeps the QSV runtime sentence, so a chip with no runtime is not blamed on the driver.
func TestDecodeKeepsTheQSVRuntimeSentence(t *testing.T) {
	c := Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"}
	if got := stepReason(context.Background(), context.Background(), c,
		errString("run ffmpeg: ffmpeg exited with code 171: Error parsing global options: Unknown error occurred"), DecodeTimeout); got != "no QSV runtime for this chip on this system" {
		t.Errorf("decode reason = %q, want the QSV runtime sentence", got)
	}
}

// A QSV message on a non-QSV card is not read as a missing QSV runtime: the family decides.
func TestPlainReasonQSVTextOnAnotherFamilyIsNotQSV(t *testing.T) {
	c := Candidate{Encoder: "hevc_nvenc", Family: encode.FamilyNVENC, Codec: "hevc"}
	if got := plainReason(c, errString("Error parsing global options: Unknown error occurred")); strings.Contains(got, "QSV") {
		t.Errorf("an NVENC card got the QSV sentence: %q", got)
	}
}

// Every probe step, not only decode, reads a budget cut-off as the probe running out, never as the driver refusing.
func TestEveryProbeStepSaysItRanOutOfBudget(t *testing.T) {
	defer func(d time.Duration) { ProbeBudget = d }(ProbeBudget)
	defer func(d time.Duration) { DecodeTimeout = d }(DecodeTimeout)
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	ProbeBudget = 80 * time.Millisecond
	DecodeTimeout = 10 * time.Second
	cases := []struct {
		name string
		hang int // the Run call that hangs
		key  string
	}{
		{"scale", runScale, "scale"},
		{"10-bit encode", runEncode10, "encode10"},
		{"10-bit decode", runDecode10, "decode10"},
		{"HDR survival", runHDREncode, "hdr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
			m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
			runs := make([]fake.RunScript, 7)
			runs[tc.hang] = fake.RunScript{Hang: true}
			m.Runs = runs
			rec := Probe(context.Background(), m, dir, c)
			got := rec.Reasons[tc.key]
			if !strings.Contains(got, "ran out of its") {
				t.Errorf("%s reason = %q; want the budget, not a driver refusal", tc.key, got)
			}
			if strings.Contains(got, "driver refused") {
				t.Errorf("%s read a budget cut-off as the driver refusing: %q", tc.key, got)
			}
		})
	}
}

// The clip and the HDR steps also say the budget ended them, not the driver (the same helper as every other step).
func TestClipAndHDRStepsSayTheBudgetEndedThem(t *testing.T) {
	defer func(d time.Duration) { ProbeBudget = d }(ProbeBudget)
	defer func(d time.Duration) { DecodeTimeout = d }(DecodeTimeout)
	ProbeBudget = 80 * time.Millisecond
	DecodeTimeout = 10 * time.Second
	cases := []struct {
		name string
		hang int
		key  string
	}{
		{"test clip", runClip, "clip"},
		{"HDR source", runHDRSource, "hdr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
			dir := t.TempDir()
			c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
			m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
			runs := make([]fake.RunScript, 7)
			runs[tc.hang] = fake.RunScript{Hang: true}
			m.Runs = runs
			rec := Probe(context.Background(), m, dir, c)
			if got := rec.Reasons[tc.key]; !strings.Contains(got, "ran out of its") {
				t.Errorf("%s reason = %q; want the budget", tc.key, got)
			}
		})
	}
}

// The HDR read stops with the budget too: the reader gives up when the probe's context ends.
func TestHDRReadSaysTheBudgetEndedIt(t *testing.T) {
	defer func(d time.Duration) { ProbeBudget = d }(ProbeBudget)
	ProbeBudget = 80 * time.Millisecond
	old := frameHDR
	frameHDR = func(ctx context.Context, _ string) (media.HDR, error) {
		<-ctx.Done()
		return media.HDR{}, ctx.Err()
	}
	defer func() { frameHDR = old }()
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi"), dir, c)
	if got := rec.Reasons["hdr"]; !strings.Contains(got, "ran out of its") {
		t.Errorf("hdr reason = %q; want the budget", got)
	}
}
