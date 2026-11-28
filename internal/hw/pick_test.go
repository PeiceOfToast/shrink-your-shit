// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
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

// A set bitrate is not built for the cards yet (L-HW-10), so no card is offered for it whatever its
// probes passed; the processor is the encoder that honours the chosen number (B1, #880).
func TestPickRefusesTheCardForABitrate(t *testing.T) {
	d := device(encode.FamilyQSV, "hevc", nil)
	got := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d}, Need{Codec: encode.CodecHEVC, FullOK: true, Bitrate: true})
	if len(got.Rungs) != 0 || got.Line != "" {
		t.Fatalf("Pick(bitrate) = %+v, want no card rungs", got)
	}
	if got.Reason != "a set bitrate isn't built for the graphics card yet" {
		t.Errorf("reason = %q, want the bitrate sentence", got.Reason)
	}
	// The same card is still offered without the bitrate: the refusal is the plan's, not the card's.
	plain := Pick(Inventory{Verdicts: []Verdict{available(d)}}, []DeviceRecord{d}, Need{Codec: encode.CodecHEVC, FullOK: true})
	if len(plain.Rungs) == 0 {
		t.Error("Pick(no bitrate) offered no rungs; want the card")
	}
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

// A card family paused after repeated failures (pause.go, #904's follow-up) is passed over, and the next card takes
// the job; Skipped says which encoder was left out and until when, so the job log can say it in words.
func TestPickPassesOverAPausedFamily(t *testing.T) {
	qsv := device(encode.FamilyQSV, "hevc", nil)
	nv := device(encode.FamilyNVENC, "hevc", nil)
	until := time.Date(2026, 11, 27, 14, 5, 0, 0, time.Local)
	inv := Inventory{Verdicts: []Verdict{available(qsv), available(nv)}}
	got := Pick(inv, []DeviceRecord{qsv, nv}, Need{Codec: encode.CodecHEVC, FullOK: true,
		Paused: func(f encode.Family) (time.Time, bool) { return until, f == encode.FamilyQSV }})
	if len(got.Rungs) == 0 || got.Rungs[0].Family != encode.FamilyNVENC {
		t.Fatalf("Pick with a paused QSV = %+v; want the NVIDIA card", got)
	}
	want := "hevc_qsv is paused until 14:05 after repeated failures"
	if len(got.Skipped) != 1 || got.Skipped[0] != want {
		t.Errorf("Skipped = %q; want [%q]", got.Skipped, want)
	}
	// With nothing paused the first card is offered again, and nothing is reported as skipped.
	free := Pick(inv, []DeviceRecord{qsv, nv}, Need{Codec: encode.CodecHEVC, FullOK: true,
		Paused: func(encode.Family) (time.Time, bool) { return time.Time{}, false }})
	if len(free.Rungs) == 0 || free.Rungs[0].Family != encode.FamilyQSV || len(free.Skipped) != 0 {
		t.Errorf("Pick with nothing paused = %+v; want the Intel card and no skips", free)
	}
}

// The pause is read through pause.go and it is a time, not a switch: once its hour is over the card is offered
// again. The clock is hw's own, so the test never waits on time.Now.
func TestPickOffersAPausedCardAgainWhenThePauseEnds(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	p := NewPauses(db, func() time.Time { return now }, nil)
	ctx := context.Background()
	for i := 0; i < FailLimit; i++ {
		if _, err := p.Record(ctx, "local", encode.FamilyQSV, false); err != nil {
			t.Fatal(err)
		}
	}
	qsv := device(encode.FamilyQSV, "hevc", nil)
	nv := device(encode.FamilyNVENC, "hevc", nil)
	inv := Inventory{Verdicts: []Verdict{available(qsv), available(nv)}}
	need := func() Need {
		return Need{Codec: encode.CodecHEVC, FullOK: true, Paused: p.Reader("local")}
	}
	if got := Pick(inv, []DeviceRecord{qsv, nv}, need()); len(got.Rungs) == 0 || got.Rungs[0].Family != encode.FamilyNVENC {
		t.Fatalf("during the pause = %+v; want the NVIDIA card", got)
	}
	now = now.Add(PauseFor + time.Minute)
	if got := Pick(inv, []DeviceRecord{qsv, nv}, need()); len(got.Rungs) == 0 || got.Rungs[0].Family != encode.FamilyQSV || len(got.Skipped) != 0 {
		t.Fatalf("after the pause = %+v; want the Intel card again", got)
	}
}

// A pause store that cannot be read counts as "not paused" — a bookkeeping problem must not keep every card out of
// work — and pause.go says so in the log, once for the same error rather than once per file. The clock is hw's own.
func TestPickUsesTheCardWhenThePauseCannotBeRead(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	sink := &eventSink{}
	p := NewPauses(db, func() time.Time { return now }, sink)
	ctx := context.Background()
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DROP TABLE encoder_state")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	qsv := device(encode.FamilyQSV, "hevc", nil)
	inv := Inventory{Verdicts: []Verdict{available(qsv)}}
	need := Need{Codec: encode.CodecHEVC, FullOK: true, Paused: p.Reader("local")}
	for i := 0; i < 3; i++ {
		if got := Pick(inv, []DeviceRecord{qsv}, need); len(got.Rungs) == 0 {
			t.Fatalf("file %d: a broken pause store refused the card: %+v", i+1, got)
		}
	}
	if len(sink.seen) != 1 {
		t.Fatalf("warnings = %d, want one for the same error across files", len(sink.seen))
	}
	if e := sink.seen[0]; e.Source != "hw" || e.Level != events.LevelWarn || !strings.Contains(e.Message, "could not be read") {
		t.Errorf("warning = %+v, want source hw, a warning, and the unreadable pause named", e)
	}
	// A minute later the same error is worth saying again: the pause may have been broken for a while.
	now = now.Add(2 * time.Minute)
	Pick(inv, []DeviceRecord{qsv}, need)
	if len(sink.seen) != 2 {
		t.Errorf("warnings = %d after a minute, want the same error said again", len(sink.seen))
	}
}
