// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// L2: a graphics device's measured record travels in the capability list as kind "device": its decode per codec
// and bit depth, and whether its scale runs on the card.
func TestDeviceRecordIsInTheCapabilityList(t *testing.T) {
	tools := doctor.Tools{Hardware: []string{"hevc_qsv"}, Devices: []doctor.DeviceRecord{{
		Family: "qsv",
		Decode: map[string]doctor.Bits{"h264": {Bit8: true}, "hevc": {Bit8: true, Bit10: false}},
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
	if got := dec["hevc"]; !got.Bit8 || got.Bit10 {
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
