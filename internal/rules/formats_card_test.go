// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

// F3: a graphics family's quality offset, by slider position. Only QSV is measured; every entry is 0 until then.
func TestCardOffsetLookups(t *testing.T) {
	for _, fam := range []string{"qsv", "nvenc", "vaapi", "amf"} {
		for q := 1; q <= 5; q++ {
			if got := CardOffset(fam, q); got != 0 {
				t.Errorf("CardOffset(%s, %d) = %d, want 0 (not measured yet)", fam, q, got)
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

// A plan with no measured offset carries no CardCRF, so its JSON (and fingerprint) is what it was before F3.
func TestZeroOffsetsLeavePlanUnchanged(t *testing.T) {
	for q := 1; q <= 5; q++ {
		if m := cardCRFFor(q); m != nil {
			t.Errorf("cardCRFFor(%d) = %v, want nil while every offset is 0", q, m)
		}
	}
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
