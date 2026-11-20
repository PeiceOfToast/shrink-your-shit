// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// The 10-bit result of each graphics encoder travels in its capability as tenBit: HDR decides from it on the
// worker. A 10-bit encoder reports true; an 8-bit-only one reports false.
func TestWorkerCapabilitiesCarryTenBit(t *testing.T) {
	tools := doctor.Tools{
		Hardware:   []string{"hevc_nvenc", "hevc_qsv"},
		Hardware10: []string{"hevc_nvenc"},
	}
	tenBit := map[string]any{}
	for _, c := range WorkerCapabilities(tools, true) {
		if key, _ := c["key"].(string); key == "encoder:hevc_nvenc" || key == "encoder:hevc_qsv" {
			tenBit[key] = c["tenBit"]
		}
	}
	if tenBit["encoder:hevc_nvenc"] != true || tenBit["encoder:hevc_qsv"] != false {
		t.Errorf("tenBit %v: want nvenc true (10-bit passed) and qsv false (8-bit only)", tenBit)
	}
}
