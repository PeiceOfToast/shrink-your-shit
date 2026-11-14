// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// capabilityByKey returns the capability with key, or nil.
func capabilityByKey(caps []map[string]any, key string) map[string]any {
	for _, c := range caps {
		if c["key"] == key {
			return c
		}
	}
	return nil
}

// A graphics card that does HEVC but not AV1 in the same family marks AV1 "unsupported", not "failed".
// Each encoder row names its format; a graphics row also names its family.
func TestCapabilitiesMarksAFormatTheCardLacksAsUnsupported(t *testing.T) {
	caps := capabilities(doctor.Tools{
		Hardware:       []string{"hevc_vaapi"},
		HardwareListed: []string{"av1_vaapi"},
		Software:       []string{"libx265"},
		TestedAt:       time.Now(),
	}, true)

	av1 := capabilityByKey(caps, "encoder:av1_vaapi")
	if av1 == nil || av1["test"] != "unsupported" || av1["detail"] != "Not supported by this graphics card." {
		t.Fatalf("av1 on a card that has hevc: %+v", av1)
	}
	if av1["format"] != "av1" || av1["family"] != "vaapi" || av1["slot"] != "gpu" {
		t.Errorf("av1 row names its format and family: %+v", av1)
	}
	hevc := capabilityByKey(caps, "encoder:hevc_vaapi")
	if hevc == nil || hevc["test"] != "passed" || hevc["format"] != "hevc" || hevc["family"] != "vaapi" {
		t.Errorf("hevc row: %+v", hevc)
	}
	sw := capabilityByKey(caps, "encoder:libx265")
	if sw == nil || sw["format"] != "hevc" {
		t.Errorf("software row names its format: %+v", sw)
	}
	if _, has := sw["family"]; has {
		t.Errorf("a processor encoder has no graphics family: %+v", sw)
	}
}

// When no format of a graphics family passed, each failed format stays "failed".
func TestCapabilitiesKeepsFailedWhenNothingOfTheFamilyPassed(t *testing.T) {
	caps := capabilities(doctor.Tools{
		HardwareListed: []string{"hevc_qsv", "h264_qsv", "av1_qsv"},
		TestedAt:       time.Now(),
	}, true)
	for _, e := range []string{"hevc_qsv", "h264_qsv", "av1_qsv"} {
		c := capabilityByKey(caps, "encoder:"+e)
		if c == nil || c["test"] != "failed" {
			t.Errorf("%s with no passed format of its family: %+v", e, c)
		}
	}
}
