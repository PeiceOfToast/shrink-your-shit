// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"path/filepath"
	"strings"
)

// Seams for VAAPIDevice: the DRM render nodes, and a node's PCI vendor as sysfs writes it. Tests swap
// them; the real ones read /dev/dri and /sys.
var (
	renderNodes = func() ([]string, error) { return filepath.Glob("/dev/dri/renderD*") }
	pciVendor   = func(node string) string {
		b, err := os.ReadFile(filepath.Join("/sys/class/drm", filepath.Base(node), "device", "vendor"))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
)

// VAAPIDevice is the render node VAAPI encodes on. A machine with an NVIDIA card and an Intel or AMD
// one has its VAAPI on the Intel or AMD node, so the first of those wins; else the first node; else
// /dev/dri/renderD128, as on most machines.
func VAAPIDevice() string {
	nodes, _ := renderNodes()
	return ChooseVAAPI(nodes, pciVendor)
}

// ChooseVAAPI is VAAPIDevice's choice among the given render nodes, with vendor giving each one's PCI vendor id.
func ChooseVAAPI(nodes []string, vendor func(string) string) string {
	if len(nodes) == 0 {
		return "/dev/dri/renderD128"
	}
	for _, n := range nodes {
		if v := vendor(n); v == "0x8086" || v == "0x1002" { // Intel, AMD
			return n
		}
	}
	return nodes[0]
}
