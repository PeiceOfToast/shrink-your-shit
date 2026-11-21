// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"testing"
)

// ProfileFor answers for the processor and for the families that have a profile; any other encoder has none, so
// its arguments stay with the switches in plan.go.
func TestProfileForLookup(t *testing.T) {
	for _, enc := range []string{"", "cpu"} {
		if p, ok := ProfileFor(enc); !ok || p.Family != "" {
			t.Errorf("ProfileFor(%q) = %+v, %v; want the processor", enc, p, ok)
		}
	}
	if p, ok := ProfileFor("av1_nvenc"); !ok || p.Family != "nvenc" {
		t.Errorf("ProfileFor(av1_nvenc) = %+v, %v; want nvenc", p, ok)
	}
	if p, ok := ProfileFor("hevc_qsv"); !ok || p.Family != "qsv" {
		t.Errorf("ProfileFor(hevc_qsv) = %+v, %v; want qsv", p, ok)
	}
	if p, ok := ProfileFor("h264_vaapi"); !ok || p.Family != "vaapi" {
		t.Errorf("ProfileFor(h264_vaapi) = %+v, %v; want vaapi", p, ok)
	}
	if p, ok := ProfileFor("h264_amf"); !ok || p.Family != "amf" {
		t.Errorf("ProfileFor(h264_amf) = %+v, %v; want amf", p, ok)
	}
	for _, enc := range []string{"hevc_videotoolbox", "libx265", "hevc_bogus", "nvenc"} {
		if p, ok := ProfileFor(enc); ok {
			t.Errorf("ProfileFor(%q) = %+v; want no profile", enc, p)
		}
	}
}

// nvenc's profile gives the quality and the 10-bit pixel format the plan used before the move.
func TestNvencProfileMatchesTheSwitches(t *testing.T) {
	p, _ := ProfileFor("hevc_nvenc")
	if got := p.quality("v:0", 22); !reflect.DeepEqual(got, []string{"-rc:v:0", "vbr", "-cq:v:0", "22"}) {
		t.Errorf("quality(v:0, 22) = %v", got)
	}
	if p.PixFmt10 != "p010le" {
		t.Errorf("PixFmt10 = %q", p.PixFmt10)
	}
}
