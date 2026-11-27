// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestHardwareInventoryOnTheMachine runs on a real machine, not in CI: it proves every candidate with real test encodes, probes
// each one that passes, and prints the inventory. It runs only with SYP_HW_INVENTORY=1, from a cross-built test binary:
//
//	GOOS=linux GOARCH=amd64 go test -c -o hwtest ./internal/hw/
//	SYP_HW_INVENTORY=1 ./hwtest -test.run TestHardwareInventoryOnTheMachine -test.v
func TestHardwareInventoryOnTheMachine(t *testing.T) {
	if os.Getenv("SYP_HW_INVENTORY") != "1" {
		t.Skip("set SYP_HW_INVENTORY=1 on a machine with a GPU to print the inventory")
	}
	// The same tool folder syp uses: an installed machine's /opt/syp/ffmpeg, with its lib folder (S3-1c).
	media.SetToolDir(os.Getenv("SYP_FFMPEG_DIR"))
	m := media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run}
	dir := t.TempDir()
	nodes := ReadRenderNodes("/sys/class/drm", "/dev/dri")
	inv := Detect(context.Background(), m, dir, Candidates(nodes))
	fmt.Println("render nodes:", nodeList(nodes))
	for _, v := range inv.Verdicts {
		if v.Available {
			fmt.Printf("%-12s available  (device %s)\n", v.Encoder, v.Device)
		} else {
			fmt.Printf("%-12s NOT available: %s\n", v.Encoder, v.Reason)
		}
	}
	fmt.Println("summary:", Summary(inv))
	for _, v := range inv.Verdicts {
		if !v.Available {
			continue
		}
		rec := Probe(context.Background(), m, dir, v.Candidate)
		fmt.Printf("probe %-12s decode=%v scale=%v encode10=%v decode10=%v hdr=%v reasons=%v\n",
			v.Encoder, rec.Decode, rec.Scale, rec.Encode10, rec.Decode10, rec.HDRSurvives, rec.Reasons)
	}
}

func nodeList(nodes []RenderNode) string {
	var parts []string
	for _, n := range nodes {
		parts = append(parts, fmt.Sprintf("%s(%s)", n.Path, n.Vendor))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
