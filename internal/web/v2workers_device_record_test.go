// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// L2: a graphics device's measured record travels in the capability list as kind "device": its decode per codec
// and bit depth, and whether its scale runs on the card.
func TestDeviceRecordIsInTheCapabilityList(t *testing.T) {
	no := false
	tools := doctor.Tools{Hardware: []string{"hevc_qsv"}, Devices: []doctor.DeviceRecord{{
		Family: "qsv",
		Decode: map[string]doctor.Bits{"h264": {Bit8: true}, "hevc": {Bit8: true, Bit10: &no}},
		Scale:  true,
	}}}
	caps := capabilities(tools, true)
	dev := capabilityByKey(caps, "device:qsv")
	if dev == nil {
		t.Fatalf("no device:qsv in %v", caps)
	}
	if dev["kind"] != "device" || dev["scale"] != true || dev["family"] != "qsv" {
		t.Errorf("device entry %v", dev)
	}
	dec, _ := dev["decode"].(map[string]doctor.Bits)
	if got := dec["hevc"]; !got.Bit8 || got.TenBit() {
		t.Errorf("hevc decode %+v, want 8-bit yes and 10-bit no", got)
	}
}

// An unmeasured card has no device entry, and the list is what it was before L2 (no processor entry).
func TestUnmeasuredCardHasNoDeviceEntry(t *testing.T) {
	caps := capabilities(doctor.Tools{Hardware: []string{"hevc_qsv"}}, true)
	if capabilityByKey(caps, "device:qsv") != nil {
		t.Error("a card with no measured record has a device entry")
	}
	for _, c := range caps {
		if c["kind"] == "device" {
			t.Errorf("unexpected device entry %v", c["key"])
		}
	}
}

// An older remote worker sends no record ("{}"): it is stored as an empty list, as before, and its own
// encoder list still reads the same.
func TestOldWorkerWithNoRecordStillReads(t *testing.T) {
	got, ok := reportedCaps(json.RawMessage(`{}`))
	if !ok || got != "{}" {
		t.Errorf("old worker's empty report: %q ok=%v", got, ok)
	}
	list := json.RawMessage(`[{"key":"encoder:libx265","kind":"encoder","test":"passed"}]`)
	if got, ok := reportedCaps(list); !ok || got == "{}" {
		t.Errorf("an encoder-only list (no record) was refused: %q ok=%v", got, ok)
	}
}

// The report limit (8 KiB) is checked on the capability list a remote worker sends. The worst cases the list can
// reach: every encoder this product names listed as passed (graphics ones at 10 bits too), or every graphics encoder
// listed as not present (the longest detail text), and a measured record for each full-pipeline family with every
// field set. Each stored report must still be accepted, with room to spare.
func TestWorstCaseCapabilitiesFitTheReportLimit(t *testing.T) {
	names := make([]string, 0, len(encoderLabels))
	for e := range encoderLabels {
		names = append(names, e)
	}
	sort.Strings(names)
	yes := true
	all := map[string]doctor.Bits{"h264": {Bit8: true}, "hevc": {Bit8: true, Bit10: &yes}}
	for _, shape := range []string{"passed", "not present"} {
		tools := doctor.Tools{TestedAt: time.Now(), FFprobe: "/usr/bin/ffprobe", FFprobeVersion: "7.1.5", SSIM: true}
		for _, e := range names {
			graphics := strings.HasSuffix(e, "_qsv") || strings.HasSuffix(e, "_vaapi") || strings.HasSuffix(e, "_nvenc") ||
				strings.HasSuffix(e, "_amf") || strings.HasSuffix(e, "_videotoolbox")
			switch {
			case graphics && shape == "passed":
				tools.Hardware = append(tools.Hardware, e)
				tools.Hardware10 = append(tools.Hardware10, e)
			case graphics:
				tools.HardwareListed = append(tools.HardwareListed, e)
			default:
				tools.Software = append(tools.Software, e)
			}
		}
		tools.Devices = []doctor.DeviceRecord{
			{Family: "qsv", Decode: all, Scale: true, HDRMeta: true},
			{Family: "vaapi", Decode: all, Scale: true, HDRMeta: true},
		}
		list := WorkerCapabilities(tools, true)
		raw, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		stored, ok := reportedCaps(raw)
		if !ok {
			t.Fatalf("%s: the worst-case report is refused: %d bytes", shape, len(raw))
		}
		t.Logf("%s: %d capability entries, %d bytes as sent, %d bytes stored", shape, len(list), len(raw), len(stored))
		if len(raw) > 8<<10-512 {
			t.Errorf("%s: worst case is %d bytes: less than 512 bytes under the 8 KiB limit", shape, len(raw))
		}
	}
}
