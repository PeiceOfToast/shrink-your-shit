// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// deviceBudget bounds the whole device record of one graphics family set (decode and scale probes): a probe
// that runs past it says "can't", and the test run ends in time.
const deviceBudget = 90 * time.Second

// Bits is whether a thing works at 8 bits and at 10 bits.
type Bits struct {
	Bit8  bool `json:"8bit"`
	Bit10 bool `json:"10bit"`
}

// DeviceRecord is what one graphics device measured can do, by small test runs: Decode per codec (h264, hevc:
// the family's hardware decoder reads a one-frame sample) and Scale (the family's scale filter runs on the
// device after an on-device decode). Family is the device's short name ("qsv", "vaapi").
type DeviceRecord struct {
	Family string
	Decode map[string]Bits
	Scale  bool
}

// deviceTest measures each graphics family among encoders (the hardware encoders that passed) on this machine.
// It is called with the encoder test's lock released, one run at a time, and never fails: a probe that fails,
// times out or is cancelled means "can't", and the record is then as far as the budget allowed.
func (c *Checker) deviceTest(ctx context.Context, encoders []string) []DeviceRecord {
	var out []DeviceRecord
	seen := map[string]bool{}
	dctx, cancel := context.WithTimeout(ctx, deviceBudget)
	defer cancel()
	for _, enc := range encoders {
		fam := familyOf(enc)
		if fam == "" || seen[fam] {
			continue
		}
		seen[fam] = true
		p, _ := rules.ProfileFor("hevc_" + fam)
		if !p.FullPipeline() {
			continue
		}
		r := DeviceRecord{Family: fam, Decode: map[string]Bits{}}
		r.Decode["h264"] = Bits{Bit8: c.Decodes(dctx, fam, "h264", false)}
		hevc := Bits{Bit8: c.Decodes(dctx, fam, "hevc", false)}
		hevc.Bit10 = c.Decodes(dctx, fam, "hevc", true)
		r.Decode["hevc"] = hevc
		r.Scale = c.scales(dctx, fam, p)
		out = append(out, r)
	}
	return out
}

// scales says whether the family's scale filter runs on the device: an on-device decode of an 8-bit H.264
// sample, scaled to a small height, then discarded. Not cached: the device record is cached as a whole.
func (c *Checker) scales(ctx context.Context, fam string, p rules.EncoderProfile) bool {
	vf := p.Scale(240, false)
	if vf == "" {
		return false
	}
	ok, _ := c.onDevice(ctx, fam, "h264", false, vf)
	return ok
}

// familyOf is the graphics family of a hardware encoder name ("hevc_qsv" -> "qsv"), "" for none.
func familyOf(enc string) string {
	i := strings.LastIndex(enc, "_")
	if i < 0 {
		return ""
	}
	return enc[i+1:]
}

// withDevices is t with the device record d.
func (t Tools) withDevices(d []DeviceRecord) Tools {
	t.Devices = d
	return t
}
