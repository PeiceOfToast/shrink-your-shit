// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
)

// device is a card that passed every probe step for one codec, with the steps a test says failed
// turned off.
func device(f encode.Family, codec string, fix func(*DeviceRecord)) DeviceRecord {
	d := DeviceRecord{
		Candidate:   Candidate{Encoder: string(encode.Codec(codec)) + "_" + familySuffix(f), Family: f, Codec: codec},
		Decode:      true,
		Scale:       true,
		ScaleOn:     "card",
		Encode10:    true,
		Decode10:    true,
		HDRSurvives: true,
	}
	if f == encode.FamilyNVENC {
		d.ScaleOn = "processor"
	}
	if fix != nil {
		fix(&d)
	}
	return d
}

func familySuffix(f encode.Family) string {
	switch f {
	case encode.FamilyQSV:
		return "qsv"
	case encode.FamilyVAAPI:
		return "vaapi"
	case encode.FamilyNVENC:
		return "nvenc"
	}
	return string(f)
}

func available(d DeviceRecord) Verdict {
	return Verdict{Candidate: d.Candidate, Available: true}
}

// The full pipeline is offered only for a card whose decode and scale both passed (L-HW-11, the
// lead's 11-28 ruling on #872).
func TestPickFullPipelineOnlyWhenDecodeAndScalePassed(t *testing.T) {
	for _, f := range []encode.Family{encode.FamilyQSV, encode.FamilyVAAPI} {
		t.Run(string(f), func(t *testing.T) {
			cases := []struct {
				name string
				fix  func(*DeviceRecord)
				full bool
			}{
				{"both passed", nil, true},
				{"decode failed", func(d *DeviceRecord) { d.Decode = false }, false},
				{"scale failed", func(d *DeviceRecord) { d.Scale = false }, false},
			}
			for _, tc := range cases {
				fix := tc.fix
				d := device(f, "hevc", fix)
				// Each condition is its own named test, so a refusal that stops being refused is named.
				t.Run(tc.name, func(t *testing.T) {
					got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
						Need{Codec: encode.CodecHEVC, FullOK: true})
					if len(got.Rungs) == 0 {
						t.Fatalf("%s: no rung", tc.name)
					}
					if isFull := got.Rungs[0].Decode == encode.DecodeHardware; isFull != tc.full {
						t.Errorf("%s: first rung decode = %q, want full = %v", tc.name, got.Rungs[0].Decode, tc.full)
					}
					// Whatever the full pipeline's answer, the hybrid rung still encodes on the card.
					last := got.Rungs[len(got.Rungs)-1]
					if last.Decode != encode.DecodeSoftware || last.Family != f {
						t.Errorf("%s: last rung = %+v, want the card's hybrid rung", tc.name, last)
					}
				})
			}
		})
	}
}

// A ten-bit job uses a card only when its ten-bit encode passed (L-HW-9).
func TestPickTenBitOnlyWhenEncode10Passed(t *testing.T) {
	d := device(encode.FamilyQSV, "hevc", func(d *DeviceRecord) { d.Encode10 = false })
	got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
		Need{Codec: encode.CodecHEVC, TenBit: true})
	if len(got.Rungs) != 0 {
		t.Fatalf("rungs = %+v, want none: the ten-bit test did not pass", got.Rungs)
	}
	if !strings.Contains(got.Reason, "10-bit") {
		t.Errorf("reason = %q, want it to name the ten-bit test", got.Reason)
	}

	d = device(encode.FamilyQSV, "hevc", nil)
	got = Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
		Need{Codec: encode.CodecHEVC, TenBit: true})
	if len(got.Rungs) == 0 || got.Rungs[0].Encoder != "hevc_qsv" {
		t.Fatalf("rungs = %+v, want the card's rung", got.Rungs)
	}
}

// An HDR job uses a card only when its HDR probe passed (L-HW-9).
func TestPickHDROnlyWhenTheHDRProbePassed(t *testing.T) {
	d := device(encode.FamilyVAAPI, "hevc", func(d *DeviceRecord) { d.HDRSurvives = false })
	got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
		Need{Codec: encode.CodecHEVC, TenBit: true, HDR: true})
	if len(got.Rungs) != 0 {
		t.Fatalf("rungs = %+v, want none: HDR did not survive the probe", got.Rungs)
	}
	if !strings.Contains(got.Reason, "HDR") {
		t.Errorf("reason = %q, want it to name HDR", got.Reason)
	}

	d = device(encode.FamilyVAAPI, "hevc", nil)
	got = Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
		Need{Codec: encode.CodecHEVC, TenBit: true, HDR: true})
	if len(got.Rungs) == 0 {
		t.Fatal("no rung for a card that kept HDR")
	}
}

// A scale that runs on the card is needed before a QSV hybrid rung scales; VAAPI's and NVIDIA's
// hybrid rungs scale on the processor, so their probe's scale step does not stand in the way.
func TestPickScaleRequiredOnlyWhereTheCardScales(t *testing.T) {
	q := device(encode.FamilyQSV, "hevc", func(d *DeviceRecord) { d.Scale = false })
	got := Pick(Inventory{Verdicts: []Verdict{available(q)}}, []DeviceRecord{q},
		Need{Codec: encode.CodecHEVC, Scales: true})
	if len(got.Rungs) != 0 {
		t.Fatalf("QSV rungs = %+v, want none: its scale is on the card", got.Rungs)
	}

	for _, f := range []encode.Family{encode.FamilyVAAPI, encode.FamilyNVENC} {
		d := device(f, "hevc", func(d *DeviceRecord) { d.Scale = false })
		got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
			Need{Codec: encode.CodecHEVC, Scales: true})
		if len(got.Rungs) == 0 {
			t.Errorf("%s: no rung; its scale runs on the processor", f)
		}
	}
}

// No card for the codec reads in the probe's own words: on a card that proved another encoder,
// "Conversion failed!" is "this card has no AV1 encoder".
func TestPickNoCardUsesTheProbeReason(t *testing.T) {
	inv := Inventory{Verdicts: []Verdict{
		{Candidate: Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"}, Available: true},
		{Candidate: Candidate{Encoder: "av1_qsv", Family: encode.FamilyQSV, Codec: "av1"},
			Reason: "this card has no AV1 encoder"},
	}}
	got := Pick(inv, []DeviceRecord{device(encode.FamilyQSV, "hevc", nil)}, Need{Codec: encode.CodecAV1})
	if len(got.Rungs) != 0 || !strings.Contains(got.Reason, "no AV1 encoder") {
		t.Fatalf("attempt = %+v, want no rung and the probe's reason", got)
	}
}

// The friendly names are L-HW-7's.
func TestProductName(t *testing.T) {
	cases := map[encode.Family]string{
		encode.FamilyQSV:   "Intel Quick Sync",
		encode.FamilyNVENC: "NVIDIA",
		encode.FamilyVAAPI: "graphics card",
		encode.FamilyAMF:   "AMD",
		encode.FamilyCPU:   "processor",
	}
	for f, want := range cases {
		if got := ProductName(f); got != want {
			t.Errorf("ProductName(%q) = %q, want %q", f, got, want)
		}
	}
	if got := ProductName(encode.Family("videotoolbox")); got != "videotoolbox" {
		t.Errorf("an unknown family = %q, want its own code", got)
	}
}

// A card chosen for the job says so in the log line, with the friendly name and the encoder.
func TestPickLineNamesTheCard(t *testing.T) {
	d := device(encode.FamilyQSV, "hevc", nil)
	got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d},
		Need{Codec: encode.CodecHEVC, FullOK: true})
	if !strings.Contains(got.Line, "Intel Quick Sync (hevc_qsv)") {
		t.Errorf("line = %q, want the friendly name and encoder", got.Line)
	}
	if !strings.Contains(got.Line, "decodes") {
		t.Errorf("line = %q, want it to say the card decodes", got.Line)
	}
}

// With no cards at all the attempt says so in plain words.
func TestPickNoCardFound(t *testing.T) {
	got := Pick(Inventory{}, nil, Need{Codec: encode.CodecHEVC})
	if len(got.Rungs) != 0 || got.Reason != "no graphics card was found" {
		t.Fatalf("attempt = %+v", got)
	}
}
