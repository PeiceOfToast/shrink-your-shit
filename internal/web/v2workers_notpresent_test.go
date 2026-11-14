// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"io/fs"
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

// A render node is here but its vendor can't be read (sysfs unreadable): we can't say the Intel card is missing.
// Quick Sync keeps "failed" and the device wording, and the permission hint is not lost.
func TestUnknownVendorIsNotNotPresent(t *testing.T) {
	origNodes, origOpen, origDRI, origVendor := renderNodes, openRender, usesDevDRI, renderVendor
	t.Cleanup(func() { renderNodes, openRender, usesDevDRI, renderVendor = origNodes, origOpen, origDRI, origVendor })
	usesDevDRI = true
	renderNodes = func() []string { return []string{"/dev/dri/renderD128"} }
	renderVendor = func(string) string { return "" }

	openRender = func(string) error { return nil }
	caps := capabilities(doctor.Tools{HardwareListed: []string{"hevc_qsv"}, Software: []string{"libx265"}}, true)
	if qsv := capabilityByKey(caps, "encoder:hevc_qsv"); qsv == nil || qsv["test"] != "failed" || qsv["detail"] != failed {
		t.Errorf("unknown vendor, device opens: %+v", qsv)
	}

	openRender = func(path string) error { return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission} }
	caps = capabilities(doctor.Tools{HardwareListed: []string{"hevc_qsv"}, Software: []string{"libx265"}}, true)
	if qsv := capabilityByKey(caps, "encoder:hevc_qsv"); qsv == nil || qsv["test"] != "failed" || !strings.HasPrefix(qsv["detail"].(string), "Can't open /dev/dri/renderD128: the app's user isn't allowed to.") {
		t.Errorf("unknown vendor, not allowed: %+v", qsv)
	}
}

// The PATCH refuses a not_present graphics encoder with 409 encoder_not_working, as it does a failed one.
func TestPatchRefusesANotPresentEncoder(t *testing.T) {
	origNodes, origDRI, origNvidia := renderNodes, usesDevDRI, nvidiaDevicePresent
	t.Cleanup(func() { renderNodes, usesDevDRI, nvidiaDevicePresent = origNodes, origDRI, origNvidia })
	usesDevDRI = true
	renderNodes = func() []string { return nil }
	nvidiaDevicePresent = func() bool { return false }

	srv, _ := workersServerFailing(t, "hevc_nvenc")
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/capabilities/test", body: "{}", ctype: "application/json"}); code != 200 {
		t.Fatalf("capability test %d %s", code, b)
	}
	code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: `{"primaryEncoder":"hevc_nvenc"}`})
	if code != 409 || errCode(b) != "encoder_not_working" {
		t.Errorf("PATCH not_present hevc_nvenc: %d %s, want 409 encoder_not_working", code, b)
	}
}
