// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// F3: a graphics family's quality offset, by slider position. QSV is measured (11-23); the others are 0.
func TestCardOffsetLookups(t *testing.T) {
	want := map[int]int{1: -2, 2: -2, 3: -1, 4: -1, 5: -1}
	for q := 1; q <= 5; q++ {
		if got := CardOffset("qsv", q); got != want[q] {
			t.Errorf("CardOffset(qsv, %d) = %d, want %d (measured)", q, got, want[q])
		}
		for _, fam := range []string{"nvenc", "vaapi", "amf"} {
			if got := CardOffset(fam, q); got != 0 {
				t.Errorf("CardOffset(%s, %d) = %d, want 0 (not measured)", fam, q, got)
			}
		}
	}
	if got := CardOffset("nope", 3); got != 0 {
		t.Errorf("unknown family: CardOffset = %d, want 0", got)
	}
	if got := CardOffset("qsv", 9); got != 0 {
		t.Errorf("unknown position: CardOffset = %d, want 0", got)
	}
	if got := CardOffset("", 3); got != 0 {
		t.Errorf("the processor (empty family): CardOffset = %d, want 0", got)
	}
}

// Only measured families are carried: a plan names qsv's offset and no other family's.
func TestCardCRFCarriesOnlyMeasured(t *testing.T) {
	for q := 1; q <= 5; q++ {
		if m := cardCRFFor(q); !reflect.DeepEqual(m, map[string]int{"qsv": CardOffset("qsv", q)}) {
			t.Errorf("cardCRFFor(%d) = %v, want only qsv", q, m)
		}
	}
	if m := cardCRFFor(9); m != nil {
		t.Errorf("cardCRFFor(9) = %v, want nil (no such position)", m)
	}
}

// A plan with no offset carries no CardCRF, so its JSON (and fingerprint) is what it was before F3.
func TestZeroOffsetsLeavePlanUnchanged(t *testing.T) {
	b, _ := json.Marshal(VideoEncode{Codec: "libx265", CRF: 23})
	if want := `{"codec":"libx265","crf":23,"preset":"","tag":""}`; string(b) != want {
		t.Errorf("VideoEncode JSON = %s, want %s", b, want)
	}
}

// A measured offset reaches the card's quality argument (and only that family's), and not the processor's.
func TestCardOffsetReachesCardArgs(t *testing.T) {
	saved := CardOffsets["qsv"]
	t.Cleanup(func() { CardOffsets["qsv"] = saved })
	CardOffsets["qsv"] = map[int]int{1: 0, 2: 0, 3: 2, 4: 0, 5: 0}

	m := cardCRFFor(3)
	if !reflect.DeepEqual(m, map[string]int{"qsv": 2}) {
		t.Fatalf("cardCRFFor(3) = %v, want map[qsv:2]", m)
	}
	if m := cardCRFFor(2); m != nil {
		t.Errorf("cardCRFFor(2) = %v, want nil (its offset is 0)", m)
	}

	p := Plan{Video: VideoEncode{Codec: "libx265", CRF: 23, CardCRF: m}}
	card := p.ffmpegArgs("in.mkv", "out.mkv", "hevc_qsv", false)
	want := profiles["qsv"].quality("v:0", 25)
	if !containsRun(card, want) {
		t.Errorf("card args %v lack the quality args for CRF 25 (23 + 2): %v", card, want)
	}

	cpu := p.ffmpegArgs("in.mkv", "out.mkv", "", false)
	if !containsRun(cpu, []string{"-crf:v:0", strconv.Itoa(23)}) {
		t.Errorf("processor args %v do not carry CRF 23: the card offset leaked", cpu)
	}
}

// containsRun reports whether sub appears as a contiguous run in args.
func containsRun(args, sub []string) bool {
	for i := 0; i+len(sub) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

// A compiled plan carries the card offset for an HEVC target only: the offsets are measured for HEVC, so an
// H.264 (or other) target's card encodes at its plain quality value.
func TestCompiledCardCRFIsHEVCOnly(t *testing.T) {
	info := func(codec string) media.Info {
		return media.Info{Container: "matroska,webm", Ext: ".mkv", VideoCodec: codec, BitrateKbps: 6000, DurationSec: 600,
			SizeBytes: 450 << 20, Streams: []media.Stream{{Index: 0, Type: "video", Codec: codec, Width: 1280, Height: 720,
				FrameRate: 24, PixFmt: "yuv420p"}}}
	}
	for _, tc := range []struct {
		format, source string
		want           map[string]int
	}{
		{"hevc", "h264", map[string]int{"qsv": CardOffset("qsv", 1)}},
		{"h264", "hevc", nil},
	} {
		s := DefaultSelectors(1)
		s.Format = tc.format
		p := compileSelectors(Plan{Kind: KindSelectors}, s, info(tc.source), nil, false)
		if p.Skip != "" {
			t.Fatalf("%s target from %s: skipped (%s), want a conversion", tc.format, tc.source, p.SkipCode)
		}
		if !reflect.DeepEqual(p.Video.CardCRF, tc.want) {
			t.Errorf("%s target: CardCRF = %v, want %v", tc.format, p.Video.CardCRF, tc.want)
		}
	}
}
