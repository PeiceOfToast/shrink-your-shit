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
	origNodes, origOpen, origDRI := renderNodes, openRender, usesDevDRI
	t.Cleanup(func() { renderNodes, openRender, usesDevDRI = origNodes, origOpen, origDRI })
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

	// Other encoders never get the device lines, even with no device.
	renderNodes = func() []string { return nil }
	if d := detail("hevc_nvenc"); d != failed {
		t.Errorf("nvenc: %q", d)
	}

	// Off Linux (Windows Quick Sync uses D3D11, not /dev/dri) the device lines would mislead.
	usesDevDRI = false
	if d := detail("hevc_qsv"); d != failed {
		t.Errorf("not linux: %q", d)
	}
}
