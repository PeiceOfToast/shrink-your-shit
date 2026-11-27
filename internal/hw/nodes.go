// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
)

// PCI vendor ids of the GPUs VAAPI can use. NVIDIA is never picked for VAAPI (L-HW-4).
const (
	vendorIntel  = "0x8086"
	vendorAMD    = "0x1002"
	vendorNVIDIA = "0x10de"
)

// fallbackNode is the render node used when none of the listed nodes is usable (L-HW-4).
const fallbackNode = "/dev/dri/renderD128"

// RenderNode is one /dev/dri/renderD* node and the PCI vendor id of the card behind it, like "0x8086".
type RenderNode struct {
	Path   string
	Vendor string
}

// ChooseRenderNode picks the node VAAPI uses: the first Intel or AMD node; otherwise the first node that is not
// NVIDIA; otherwise /dev/dri/renderD128. NVIDIA is never picked.
func ChooseRenderNode(nodes []RenderNode) string {
	for _, n := range nodes {
		if n.Vendor == vendorIntel || n.Vendor == vendorAMD {
			return n.Path
		}
	}
	for _, n := range nodes {
		if n.Vendor != vendorNVIDIA {
			return n.Path
		}
	}
	return fallbackNode
}

// ReadRenderNodes lists the render nodes under a sysfs class folder (/sys/class/drm in production), with the vendor
// of each card. A node whose vendor cannot be read keeps an empty vendor.
func ReadRenderNodes(sysDrm string) []RenderNode {
	matches, _ := filepath.Glob(filepath.Join(sysDrm, "renderD*"))
	sort.Strings(matches)
	var out []RenderNode
	for _, m := range matches {
		name := filepath.Base(m)
		vendor, _ := os.ReadFile(filepath.Join(m, "device", "vendor"))
		out = append(out, RenderNode{
			Path:   path.Join("/dev/dri", name), // a Linux device path, whatever OS runs the check
			Vendor: strings.TrimSpace(string(vendor)),
		})
	}
	return out
}

// Candidates is the list of encoders to prove on this machine: QSV and NVENC once each, and VAAPI on every node
// that is not NVIDIA. Each is tested for HEVC, H.264 and AV1 on its own.
func Candidates(nodes []RenderNode) []Candidate {
	var out []Candidate
	for _, codec := range []string{"hevc", "h264", "av1"} {
		out = append(out, Candidate{Encoder: codec + "_qsv", Family: encode.FamilyQSV, Codec: codec})
		out = append(out, Candidate{Encoder: codec + "_nvenc", Family: encode.FamilyNVENC, Codec: codec})
		for _, n := range nodes {
			if n.Vendor == vendorNVIDIA {
				continue
			}
			out = append(out, Candidate{Encoder: codec + "_vaapi", Family: encode.FamilyVAAPI, Codec: codec, Device: n.Path})
		}
	}
	return out
}
