// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
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
// device after an on-device decode) and HDRMeta (the device's HEVC encode keeps HDR10's mastering display and
// light levels). Family is the device's short name ("qsv", "vaapi").
type DeviceRecord struct {
	Family  string
	Decode  map[string]Bits
	Scale   bool
	HDRMeta bool
}

// hdrParams are the HDR10 static metadata the sample carries: a mastering display (BT.2020 primaries, D65, 1000
// nits) and content light levels (1000 MaxCLL, 400 MaxFALL).
const hdrParams = "master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400"

// deviceTest measures each graphics family among encoders (the hardware encoders that passed) on this machine.
// tenBit are the encoders that also encoded at 10 bits; a family whose HEVC does gets the HDR probe too. It is
// called with the encoder test's lock released, one run at a time, and never fails: a probe that fails, times out
// or is cancelled means "can't", and the record is then as far as the budget allowed.
func (c *Checker) deviceTest(ctx context.Context, encoders, tenBit []string) []DeviceRecord {
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
		if slices.Contains(tenBit, "hevc_"+fam) {
			r.HDRMeta = c.hdrMeta(dctx, fam)
		}
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

// hdrMeta says whether the family's HEVC encode on the device keeps HDR10's static metadata. A 10-bit sample with
// the metadata is made in software, decoded on the device and encoded again into a Matroska file; ffprobe must then
// find both the mastering display and the light levels in the file. Any step that fails means "can't".
func (c *Checker) hdrMeta(ctx context.Context, fam string) bool {
	if c.FFmpeg == "" || c.FFprobe == "" {
		return false
	}
	dir, err := os.MkdirTemp("", "hdr-probe")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	sample, out := filepath.Join(dir, "sample.hevc"), filepath.Join(dir, "out.mkv")
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := c.Exec.Output(tctx, c.FFmpeg, "-hide_banner", "-v", "error", "-f", "lavfi", "-i",
		"color=c=black:s=256x256:d=0.1", "-frames:v", "1", "-pix_fmt", "yuv420p10le", "-c:v", "libx265",
		"-x265-params", hdrParams, "-f", "hevc", sample); err != nil {
		return false
	}
	if _, err := c.Exec.Output(tctx, c.FFmpeg, hdrEncodeArgs(fam, media.VAAPIDevice(), sample, out)...); err != nil {
		return false
	}
	js, err := c.Exec.Output(tctx, c.FFprobe, "-v", "error", "-show_streams", "-of", "json", out)
	return err == nil && hasHDRSideData(js)
}

// hdrEncodeArgs decodes in on the device with the family's full pipeline and encodes it as HEVC into out.
func hdrEncodeArgs(fam, device, in, out string) []string {
	args := []string{"-hide_banner", "-v", "error"}
	p, _ := rules.ProfileFor("hevc_" + fam)
	args = append(args, p.Setup(device)...)
	args = append(args, p.FullDecode()...)
	args = append(args, "-i", in, "-c:v", "hevc_"+fam, "-f", "matroska", out)
	return args
}

// hasHDRSideData reads ffprobe's stream JSON: true when the first video stream carries both the mastering display
// and the content light level side data.
func hasHDRSideData(js []byte) bool {
	var v struct {
		Streams []struct {
			SideData []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if json.Unmarshal(js, &v) != nil || len(v.Streams) == 0 {
		return false
	}
	var mastering, light bool
	for _, d := range v.Streams[0].SideData {
		switch d.Type {
		case "Mastering display metadata":
			mastering = true
		case "Content light level metadata":
			light = true
		}
	}
	return mastering && light
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
