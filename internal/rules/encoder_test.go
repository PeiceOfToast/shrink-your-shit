// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"strings"
	"testing"
)

// gpuCaps is a worker's capability list: each named graphics encoder passed its test, and those in tenBit also
// passed the 10-bit one.
func gpuCaps(passed []string, tenBit ...string) []map[string]any {
	out := []map[string]any{}
	for _, e := range passed {
		out = append(out, map[string]any{"key": "encoder:" + e, "kind": "encoder", "slot": "gpu", "test": "passed",
			"tenBit": slices.Contains(tenBit, e)})
	}
	return out
}

// hdrSource is an HDR10 source; withMeta adds the mastering display and light level the probe reads from frames.
func hdrSource(withMeta bool) *HDR {
	h := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	if withMeta {
		h.MasterDisplay = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1)"
		h.CLL = "1000,400"
	}
	return h
}

func TestHDRWithMasteringGPUWhenBothTestsPassAndTheFamilyIsProven(t *testing.T) {
	for _, enc := range []string{"hevc_qsv", "hevc_vaapi"} {
		got, note := EncoderFor("libx265", enc, hdrSource(true), gpuCaps([]string{enc}, enc))
		if got != enc || note != "" {
			t.Fatalf("%s with both tests passed: got %q note %q, want the GPU", enc, got, note)
		}
	}
}

func TestHDRPicksTheProcessorWhenThe10BitTestFailed(t *testing.T) {
	got, note := EncoderFor("libx265", "hevc_qsv", hdrSource(true), gpuCaps([]string{"hevc_qsv"}))
	if got != "" || !strings.Contains(note, "10-bit") || !strings.Contains(note, "hevc_qsv") {
		t.Fatalf("8-bit-only encoder for HDR: got %q note %q, want the processor with a 10-bit note", got, note)
	}
}

func TestHDRWithMasteringPicksTheProcessorForAnUnprovenFamily(t *testing.T) {
	got, note := EncoderFor("libx265", "hevc_nvenc", hdrSource(true), gpuCaps([]string{"hevc_nvenc"}, "hevc_nvenc"))
	if got != "" || !strings.Contains(note, "mastering data") || !strings.Contains(note, "hevc_nvenc") {
		t.Fatalf("nvenc with mastering data: got %q note %q, want the processor with the mastering note", got, note)
	}
}

// HLG and HDR10 without static metadata have nothing a family must carry, so a 10-bit GPU is used.
func TestHDRWithoutStaticMetadataUsesAnyTenBitGPU(t *testing.T) {
	got, note := EncoderFor("libx265", "hevc_nvenc", hdrSource(false), gpuCaps([]string{"hevc_nvenc"}, "hevc_nvenc"))
	if got != "hevc_nvenc" || note != "" {
		t.Fatalf("HDR with no mastering data on a 10-bit nvenc: got %q note %q, want the GPU", got, note)
	}
}

func TestHDRGPUNeedsItsOwnTestToHavePassed(t *testing.T) {
	got, note := EncoderFor("libx265", "hevc_qsv", hdrSource(true), gpuCaps(nil))
	if got != "" || !strings.Contains(note, "didn't pass its test") {
		t.Fatalf("no passing encoder: got %q note %q", got, note)
	}
}

// SDR plans are not changed by the HDR rules: the encoder is main's when its test passed, whatever the 10-bit
// result, for every codec and family.
func TestSDRPlansPickTheSameEncoderAsBefore(t *testing.T) {
	families := []string{"nvenc", "qsv", "vaapi", "amf"}
	codecs := []string{"libx265", "libx264", "libsvtav1"}
	for _, codec := range codecs {
		for _, fam := range families {
			prefix := formatPrefix[codec]
			main := prefix + "_" + fam
			want := GraphicsEncoder(codec, main)
			if want == "" {
				continue // no graphics encoder for this format and family
			}
			for _, passed := range [][]string{nil, {want}} {
				for _, tenBit := range [][]string{nil, {want}} {
					exp := ""
					if slices.Contains(passed, want) {
						exp = want
					}
					got, _ := EncoderFor(codec, main, nil, gpuCaps(passed, tenBit...))
					if got != exp {
						t.Fatalf("SDR %s on %s (passed %v, 10-bit %v): got %q, want %q", codec, main, passed, tenBit, got, exp)
					}
				}
			}
		}
		if got, note := EncoderFor(codec, "cpu", nil, gpuCaps([]string{"hevc_qsv"}, "hevc_qsv")); got != "" || note != "" {
			t.Fatalf("cpu main: got %q note %q, want the processor by choice", got, note)
		}
	}
}
