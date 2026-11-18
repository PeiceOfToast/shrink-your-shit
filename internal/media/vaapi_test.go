// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"errors"
	"testing"
)

// stubRender swaps the render nodes and their vendors for the test.
func stubRender(t *testing.T, nodes []string, err error, vendors map[string]string) {
	t.Helper()
	oldNodes, oldVendor := renderNodes, pciVendor
	t.Cleanup(func() { renderNodes, pciVendor = oldNodes, oldVendor })
	renderNodes = func() ([]string, error) { return nodes, err }
	pciVendor = func(node string) string { return vendors[node] }
}

func TestVAAPIDeviceSkipsNVIDIA(t *testing.T) {
	stubRender(t, []string{"/dev/dri/renderD128", "/dev/dri/renderD129"}, nil,
		map[string]string{"/dev/dri/renderD128": "0x10de", "/dev/dri/renderD129": "0x8086"})
	if got := VAAPIDevice(); got != "/dev/dri/renderD129" {
		t.Errorf("NVIDIA first, Intel second: got %s, want the Intel node", got)
	}
}

func TestVAAPIDeviceAMD(t *testing.T) {
	stubRender(t, []string{"/dev/dri/renderD128", "/dev/dri/renderD129"}, nil,
		map[string]string{"/dev/dri/renderD128": "0x10de", "/dev/dri/renderD129": "0x1002"})
	if got := VAAPIDevice(); got != "/dev/dri/renderD129" {
		t.Errorf("NVIDIA first, AMD second: got %s", got)
	}
}

func TestVAAPIDeviceOnlyNVIDIA(t *testing.T) {
	stubRender(t, []string{"/dev/dri/renderD128"}, nil, map[string]string{"/dev/dri/renderD128": "0x10de"})
	if got := VAAPIDevice(); got != "/dev/dri/renderD128" {
		t.Errorf("only NVIDIA: got %s", got)
	}
}

func TestVAAPIDeviceNoNodes(t *testing.T) {
	stubRender(t, nil, errors.New("no glob"), nil)
	if got := VAAPIDevice(); got != "/dev/dri/renderD128" {
		t.Errorf("no nodes: got %s", got)
	}
}

func TestVAAPIDeviceUnknownVendorFallsBackToFirst(t *testing.T) {
	stubRender(t, []string{"/dev/dri/renderD130", "/dev/dri/renderD131"}, nil, map[string]string{})
	if got := VAAPIDevice(); got != "/dev/dri/renderD130" {
		t.Errorf("no vendor known: got %s, want the first node", got)
	}
}
