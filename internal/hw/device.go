// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// frameHDR reads the HDR side data of a video's first frame. A variable so a test can answer for a file it cannot make.
var frameHDR = media.ProbeFrameHDR

// ProbeBudget is the whole device probe for one card (L-HW-2). A variable so a test can shorten it.
var ProbeBudget = 90 * time.Second

// DecodeTimeout bounds the decode step on its own (L-HW-2). A variable so a test can shorten it.
var DecodeTimeout = 30 * time.Second

// DeviceRecord is what a card can do for the full pipeline, step by step. Each field is true only when its step
// passed; Reasons says why a step did not, in plain words.
type DeviceRecord struct {
	Candidate
	Decode   bool   // hardware decode of the test clip
	Scale    bool   // the scale to 240p passed
	ScaleOn  string // "card" for QSV and VAAPI, "processor" for NVENC (its scale is a CPU filter), "" when not scaled
	Encode10 bool   // a 10-bit encode, recorded apart from the 8-bit ones
	Decode10 bool   // a 10-bit decode, recorded apart from the 8-bit one
	// HDRSurvives: the card's own 10-bit encode of an HDR10 source keeps both the mastering display and the content light
	// side data (L-HW-2). A card that drops them does not carry HDR.
	HDRSurvives bool
	Reasons     map[string]string
}

// Probe runs the device probe for one card inside ProbeBudget. Steps that cannot run because an earlier step failed
// are recorded as not passed with that reason. A scale failure reads as "can't", never as a crash.
func Probe(ctx context.Context, m media.Media, dir string, c Candidate) DeviceRecord {
	rec := DeviceRecord{Candidate: c, Reasons: map[string]string{}}
	pctx, cancel := context.WithTimeout(ctx, ProbeBudget)
	defer cancel()

	clip := filepath.Join(dir, "probe-clip.mkv")
	if err := makeClip(pctx, m, clip, 8); err != nil {
		rec.Reasons["clip"] = stepFailure(pctx, c, err, "the test clip could not be made: ")
		return rec
	}
	defer os.Remove(clip)

	// Decode (30 s): the card decodes the 8-bit clip.
	dctx, dcancel := context.WithTimeout(pctx, DecodeTimeout)
	_, err := m.Run(dctx, append([]string{"ffmpeg"}, decodeArgs(c, clip)...), nil)
	dcancel()
	if err != nil {
		rec.Reasons["decode"] = stepReason(pctx, dctx, err, DecodeTimeout)
	} else {
		rec.Decode = true
	}

	// Scale to 240p, then the card's 10-bit encode and decode, each only when the earlier steps passed.
	if pctx.Err() != nil {
		rec.Reasons["budget"] = fmt.Sprintf("the device probe ran out of its %d s", int(ProbeBudget.Seconds()))
		return rec
	}
	scaled := filepath.Join(dir, c.Encoder+".scale.mkv")
	defer os.Remove(scaled)
	if rec.Decode {
		if _, err := m.Run(pctx, append([]string{"ffmpeg"}, scaleArgs(c, clip, scaled)...), nil); err != nil {
			rec.Reasons["scale"] = stepFailure(pctx, c, err, "can't scale on this card: ")
		} else if info, err := m.Probe(pctx, scaled); err != nil || !height240(info) {
			rec.Reasons["scale"] = "can't scale on this card: the output is not 240p"
		} else {
			rec.Scale = true
			rec.ScaleOn = scaledOn(c)
		}
	} else {
		rec.Reasons["scale"] = "not tried: decode did not pass"
	}

	tenBit := filepath.Join(dir, c.Encoder+".10bit.mkv")
	defer os.Remove(tenBit)
	if rec.Decode {
		if _, err := m.Run(pctx, append([]string{"ffmpeg"}, encode10Args(c, clip, tenBit)...), nil); err != nil {
			rec.Reasons["encode10"] = stepFailure(pctx, c, err, "")
		} else if info, err := m.Probe(pctx, tenBit); err != nil || !namedBy(movieEncoder(info), c.Encoder) {
			rec.Reasons["encode10"] = "the 10-bit output was not written by " + c.Encoder
		} else {
			rec.Encode10 = true
		}
	} else {
		rec.Reasons["encode10"] = "not tried: decode did not pass"
	}
	if rec.Encode10 {
		if _, err := m.Run(pctx, append([]string{"ffmpeg"}, decodeArgs(c, tenBit)...), nil); err != nil {
			rec.Reasons["decode10"] = stepFailure(pctx, c, err, "")
		} else {
			rec.Decode10 = true
		}
	} else {
		rec.Reasons["decode10"] = "not tried: the 10-bit encode did not pass"
	}

	// HDR survival (L-HW-2): an HDR10 source, encoded by the card in 10 bits, must keep its side data.
	if rec.Encode10 {
		hdrSrc := filepath.Join(dir, "hdr-source.mkv")
		defer os.Remove(hdrSrc)
		hdrOut := filepath.Join(dir, c.Encoder+".hdr.mkv")
		defer os.Remove(hdrOut)
		if err := makeHDRClip(pctx, m, hdrSrc); err != nil {
			rec.Reasons["hdr"] = stepFailure(pctx, c, err, "the HDR test source could not be made: ")
		} else if _, err := m.Run(pctx, append([]string{"ffmpeg"}, hdrEncodeArgs(c, hdrSrc, hdrOut)...), nil); err != nil {
			rec.Reasons["hdr"] = stepFailure(pctx, c, err, "")
		} else if h, err := frameHDR(pctx, hdrOut); err != nil {
			rec.Reasons["hdr"] = stepFailure(pctx, c, err, "the HDR output could not be read: ")
		} else if !h.MasteringDisplay || !h.ContentLight {
			rec.Reasons["hdr"] = "the HDR metadata did not survive the card's encode"
		} else {
			rec.HDRSurvives = true
		}
	} else {
		rec.Reasons["hdr"] = "not tried: the 10-bit encode did not pass"
	}
	return rec
}

// makeHDRClip writes a short HDR10 source with libx265 and the HDR10 signalling and mastering data (no real media).
func makeHDRClip(ctx context.Context, m media.Media, out string) error {
	argv := []string{"ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-t", "1",
		"-pix_fmt", "yuv420p10le", "-c:v", "libx265",
		"-x265-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400:hdr-opt=1:repeat-headers=1",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", out}
	_, err := m.Run(ctx, argv, nil)
	return err
}

// hdrEncodeArgs is the card's 10-bit encode of the HDR source.
func hdrEncodeArgs(c Candidate, src, out string) []string {
	return append(deviceSetup(c), "-i", src, "-vf", uploadFilter(c, "p010"), "-c:v", c.Encoder, "-profile:v", "main10", "-y", out)
}

// stepReason explains a failed step: its own timeout, the whole probe running out, or the tool's words.
func stepReason(pctx, sctx context.Context, err error, limit time.Duration) string {
	if errors.Is(sctx.Err(), context.DeadlineExceeded) && pctx.Err() == nil {
		return fmt.Sprintf("the decode did not finish in %d s", int(limit.Seconds()))
	}
	if pctx.Err() != nil {
		return fmt.Sprintf("the device probe ran out of its %d s", int(ProbeBudget.Seconds()))
	}
	return plainReason(Candidate{}, err)
}

func height240(info media.Info) bool {
	for _, v := range info.Video {
		if !v.AttachedPicture {
			return v.Height == 240
		}
	}
	return false
}

func movieEncoder(info media.Info) string {
	if v, ok := movieTrack(info); ok {
		return v.Encoder
	}
	return ""
}

// makeClip writes a small black clip in the sample format for a bit depth.
func makeClip(ctx context.Context, m media.Media, out string, bits int) error {
	pix := "yuv420p"
	if bits == 10 {
		pix = "yuv420p10le"
	}
	argv := []string{"ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=black:s=256x256:d=0.1", "-pix_fmt", pix, "-c:v", "libx264", out}
	_, err := m.Run(ctx, argv, nil)
	return err
}

// The per-family argv of the device probe steps. A fourth family or a new step adds a case here.
func decodeArgs(c Candidate, clip string) []string {
	args := []string{"-nostdin", "-hide_banner"}
	switch c.Family {
	case encode.FamilyVAAPI:
		args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", c.Device)
	case encode.FamilyQSV:
		args = append(args, "-hwaccel", "qsv")
	case encode.FamilyNVENC:
		args = append(args, "-hwaccel", "cuda")
	}
	return append(args, "-i", clip, "-f", "null", "-")
}

func scaleArgs(c Candidate, clip, out string) []string {
	return append(deviceSetup(c), "-i", clip, "-vf", scaleFilter(c), "-c:v", c.Encoder, "-y", out)
}

func encode10Args(c Candidate, clip, out string) []string {
	return append(deviceSetup(c), "-i", clip, "-vf", uploadFilter(c, "p010"), "-c:v", c.Encoder, "-profile:v", "main10", "-y", out)
}

// deviceSetup is the device arguments a family needs before its input.
func deviceSetup(c Candidate) []string {
	switch c.Family {
	case encode.FamilyVAAPI:
		return []string{"-nostdin", "-hide_banner", "-vaapi_device", c.Device}
	case encode.FamilyQSV:
		return []string{"-nostdin", "-hide_banner", "-init_hw_device", "qsv=qsv:hw", "-filter_hw_device", "qsv"}
	}
	return []string{"-nostdin", "-hide_banner"}
}

// scaleFilter is the scale to 240p on the card, with the family's own filter.
func scaleFilter(c Candidate) string {
	switch c.Family {
	// The probe clip is square, so 240×240 is 240 high. Hardware scalers take explicit sizes (L-HW-10).
	case encode.FamilyVAAPI:
		return "format=nv12,hwupload,scale_vaapi=w=240:h=240:format=nv12"
	case encode.FamilyQSV:
		return "format=nv12,hwupload=extra_hw_frames=64,scale_qsv=w=240:h=240"
	}
	return "scale=240:240"
}

// uploadFilter puts a frame on the card in a pixel format.
func uploadFilter(c Candidate, fmtName string) string {
	switch c.Family {
	case encode.FamilyVAAPI:
		return "format=" + fmtName + ",hwupload"
	case encode.FamilyQSV:
		return "format=" + fmtName + ",hwupload=extra_hw_frames=64"
	}
	return "format=" + fmtName
}

// stepFailure is the reason a probe step failed. A step cut short by the whole probe's budget says so, on every step,
// so a timeout never reads as the driver refusing (prefix is added only when the step failed on its own).
func stepFailure(pctx context.Context, c Candidate, err error, prefix string) string {
	if pctx.Err() != nil {
		return fmt.Sprintf("the device probe ran out of its %d s at this step", int(ProbeBudget.Seconds()))
	}
	return prefix + plainReason(c, err)
}

// scaledOn says where a family's scale to 240p runs. NVENC's filter is the processor's, so its record says so.
func scaledOn(c Candidate) string {
	if c.Family == encode.FamilyNVENC {
		return "processor"
	}
	return "card"
}
