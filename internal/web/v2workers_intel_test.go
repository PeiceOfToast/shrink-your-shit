// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// A failed Quick Sync or VA-API test says why when the device is the cause: no /dev/dri at all, or a
// render node this user cannot open. Other failed encoders keep the generic line.
func TestIntelFailureSaysWhy(t *testing.T) {
	origNodes, origOpen, origDRI, origVendor, origNvidia := renderNodes, openRender, usesDevDRI, renderVendor, nvidiaDevicePresent
	t.Cleanup(func() {
		renderNodes, openRender, usesDevDRI, renderVendor, nvidiaDevicePresent = origNodes, origOpen, origDRI, origVendor, origNvidia
	})
	usesDevDRI = true

	detail := func(enc string) string {
		for _, c := range capabilities(doctor.Tools{HardwareListed: []string{enc}}, true) {
			if c["key"] == "encoder:"+enc {
				d, _ := c["detail"].(string)
				return d
			}
		}
		t.Fatalf("no capability for %s", enc)
		return ""
	}

	renderNodes = func() []string { return nil }
	openRender = func(string) error { return errors.New("unused") }
	if d := detail("hevc_qsv"); !strings.Contains(d, "/dev/dri is missing") || !strings.Contains(d, "docker-compose.yml") {
		t.Errorf("no device: %q", d)
	}
	if d := detail("hevc_vaapi"); !strings.Contains(d, "/dev/dri is missing") {
		t.Errorf("no device vaapi: %q", d)
	}

	renderNodes = func() []string { return []string{"/dev/dri/renderD128"} }
	openRender = func(path string) error { return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission} }
	if d := detail("hevc_qsv"); !strings.HasPrefix(d, "Can't open /dev/dri/renderD128: the app's user isn't allowed to.") || strings.Contains(d, "group_add") {
		t.Errorf("not allowed: %q", d)
	}
	// Any other open error is shown as it is, not blamed on the group.
	openRender = func(string) error { return errors.New("no such device") }
	if d := detail("hevc_qsv"); d != "Can't open /dev/dri/renderD128: no such device." {
		t.Errorf("other open error: %q", d)
	}

	openRender = func(string) error { return nil }
	if d := detail("hevc_qsv"); d != failed {
		t.Errorf("device opens, generic line wanted: %q", d)
	}

	// On a mixed machine the Intel node is the one Quick Sync opens, not the NVIDIA node listed first.
	renderNodes = func() []string { return []string{"/dev/dri/renderD128", "/dev/dri/renderD129"} }
	renderVendor = func(node string) string {
		if node == "/dev/dri/renderD128" {
			return "0x10de"
		}
		return "0x8086"
	}
	openRender = func(path string) error { return errors.New("opened " + path) }
	if d := detail("hevc_qsv"); d != "Can't open /dev/dri/renderD129: opened /dev/dri/renderD129." {
		t.Errorf("mixed machine opens the wrong node: %q", d)
	}
	renderNodes = func() []string { return nil }

	// NVENC says the card is missing when /dev/nvidia0 is, and keeps the generic line when it is there.
	nvidiaDevicePresent = func() bool { return false }
	if d := detail("hevc_nvenc"); !strings.HasPrefix(d, "No NVIDIA card here (/dev/nvidia0 is missing).") {
		t.Errorf("nvenc without the device: %q", d)
	}
	nvidiaDevicePresent = func() bool { return true }
	if d := detail("hevc_nvenc"); d != failed {
		t.Errorf("nvenc with the device: %q", d)
	}

	// Other encoders never get the device lines, even with no device.
	nvidiaDevicePresent = func() bool { return true }
	renderNodes = func() []string { return nil }
	if d := detail("hevc_amf"); d != failed {
		t.Errorf("amf: %q", d)
	}

	// Off Linux (Windows Quick Sync uses D3D11, not /dev/dri) the device lines would mislead.
	usesDevDRI = false
	if d := detail("hevc_qsv"); d != failed {
		t.Errorf("not linux: %q", d)
	}
}
