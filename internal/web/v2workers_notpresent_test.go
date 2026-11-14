// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// seamIntelCard makes this computer have an Intel render node the test can open, restored when t ends.
func seamIntelCard(t *testing.T) {
	t.Helper()
	origNodes, origOpen, origDRI, origVendor := renderNodes, openRender, usesDevDRI, renderVendor
	t.Cleanup(func() { renderNodes, openRender, usesDevDRI, renderVendor = origNodes, origOpen, origDRI, origVendor })
	usesDevDRI = true
	renderNodes = func() []string { return []string{"/dev/dri/renderD128"} }
	renderVendor = func(string) string { return vendorIntel }
	openRender = func(string) error { return nil }
}

// A graphics encoder whose card is not in this computer reads "not_present", not "failed": ffmpeg lists it,
// but there is nothing for it to run on. A card that is there and fails still reads "failed". A not_present
// encoder is never the main encoder.
func TestGraphicsEncoderWithoutItsCardIsNotPresent(t *testing.T) {
	origNodes, origDRI, origNvidia := renderNodes, usesDevDRI, nvidiaDevicePresent
	t.Cleanup(func() { renderNodes, usesDevDRI, nvidiaDevicePresent = origNodes, origDRI, origNvidia })
	usesDevDRI = true
	renderNodes = func() []string { return nil }

	nvidiaDevicePresent = func() bool { return false }
	caps := capabilities(doctor.Tools{HardwareListed: []string{"hevc_nvenc"}, Software: []string{"libx265"}}, true)
	nv := capabilityByKey(caps, "encoder:hevc_nvenc")
	if nv == nil || nv["test"] != "not_present" || !strings.HasPrefix(nv["detail"].(string), "No NVIDIA graphics card in this computer") {
		t.Errorf("nvenc without its card: %+v", nv)
	}
	if got := primaryEncoder(nil, caps); got != "cpu" {
		t.Errorf("primary with no card: %q, want cpu", got)
	}

	nvidiaDevicePresent = func() bool { return true }
	caps = capabilities(doctor.Tools{HardwareListed: []string{"hevc_nvenc"}, Software: []string{"libx265"}}, true)
	if nv := capabilityByKey(caps, "encoder:hevc_nvenc"); nv == nil || nv["test"] != "failed" {
		t.Errorf("nvenc with its card that fails: %+v", nv)
	}

	// VA-API with neither an Intel nor an AMD render node: not_present, with the family named.
	nvidiaDevicePresent = func() bool { return false }
	caps = capabilities(doctor.Tools{HardwareListed: []string{"hevc_vaapi"}, Software: []string{"libx265"}}, true)
	if va := capabilityByKey(caps, "encoder:hevc_vaapi"); va == nil || va["test"] != "not_present" || !strings.HasPrefix(va["detail"].(string), "No Intel or AMD graphics card in this computer.") {
		t.Errorf("vaapi without a render node: %+v", va)
	}
}
