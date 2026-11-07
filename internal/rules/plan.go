// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A Plan is what to do with one file: compiled from its library's flow template and the file's probe
// (TIERS-PLAN.md 1.2). The scanner asks it whether to convert, the worker runs its ffmpeg arguments
// and checks the result against its Expect. A job keeps its Plan as a snapshot, so what was intended
// can be read back later (slice 17's audit log).
type Plan struct {
	Skip            string      `json:"skip,omitempty"`   // why this file is not converted ("" = convert it)
	Reason          string      `json:"reason,omitempty"` // why it is converted, shown on the job
	Eligibility     Eligibility `json:"eligibility"`      // the rule that decided Skip (part of the fingerprint)
	Video           VideoEncode `json:"video"`
	Output          string      `json:"output"`                 // replace | beside
	MinSavingPct    int         `json:"minSavingPct,omitempty"` // the new file must be this much smaller (0: just smaller)
	MinSSIM         float64     `json:"minSSIM,omitempty"`      // picture threshold; 0 = the program's -min-ssim
	Kind            string      `json:"kind"`                   // the template kind it was compiled from
	CompilerVersion int         `json:"compilerVersion"`
}

// Eligibility is the rule a Plan decides with: which source format, from which bitrate.
type Eligibility struct {
	FromCodec      string `json:"fromCodec,omitempty"`      // legacy: this source codec only
	MinBitrateKbps int    `json:"minBitrateKbps,omitempty"` // legacy: from this bitrate
	// MinBitsPerPixel: slider, the formats in SliderSources above this many bits per pixel per frame.
	MinBitsPerPixel float64 `json:"minBitsPerPixel,omitempty"`
}

// VideoEncode is how the main video is encoded. Everything else is copied.
type VideoEncode struct {
	Codec  string `json:"codec"`           // ffmpeg encoder
	Index  int    `json:"index,omitempty"` // which video stream is encoded (0 = the first; cover art is skipped)
	CRF    int    `json:"crf"`
	Preset string `json:"preset"`
	Tag    string `json:"tag"`
	// MaxRateKbps caps the bitrate (VBV), for the Standard selectors' maxBitrate (slice 25); 0 = none.
	MaxRateKbps int `json:"maxRateKbps,omitempty"`
}

// Compile makes the Plan for one file. Skip says whether it should be converted (the scanner's
// decision); Video, Output and the thresholds say how. A template that cannot run yields a Plan
// with Skip set and no Video codec.
func (t Template) Compile(info media.Info) Plan {
	p := Plan{Kind: t.Kind, CompilerVersion: t.CompilerVersion}
	var r Rule
	switch t.Kind {
	case KindLegacy:
		pr := t.Params
		if pr.FromCodec == "" || pr.CRF <= 0 || pr.Preset == "" {
			p.Skip = fmt.Sprintf("its library's flow is missing values (%+v)", pr)
			return p
		}
		r = Rule{FromCodec: pr.FromCodec, MinBitrateKbps: pr.MinBitrateKbps, CRF: pr.CRF, Preset: pr.Preset}
		p.Output = outputOr(pr.Output)
	case KindSlider:
		pos, ok := SliderAt(t.Params.Quality)
		if !ok {
			p.Skip = fmt.Sprintf("slider position %d does not exist", t.Params.Quality)
			return p
		}
		return compileSlider(p, pos, info)
	default:
		p.Skip = fmt.Sprintf("flows of kind %q cannot run yet", t.Kind)
		return p
	}
	// How it is converted is set first, so a Plan that skips the file still says what converting
	// it would mean; whether it is converted is the scanner's decision (Skip).
	p.Eligibility = Eligibility{FromCodec: r.FromCodec, MinBitrateKbps: r.MinBitrateKbps}
	p.Video = VideoEncode{Codec: "libx265", CRF: r.CRF, Preset: r.Preset, Tag: "hvc1"}
	if ok, why := r.Match(info); ok {
		p.Reason = why
	} else {
		p.Skip = why
	}
	return p
}

// FFmpegArgs are the ffmpeg arguments that convert in to out: the first video stream is encoded as
// the Plan says, everything else (audio, subtitles, attachments, extra streams) is copied, data
// streams are dropped, and the container stays the input's. Progress goes to stdout.
func (p Plan) FFmpegArgs(in, out string) []string {
	v := "v:" + strconv.Itoa(p.Video.Index)
	args := []string{
		"-y", "-nostdin", "-v", "error", "-progress", "pipe:1", "-nostats",
		"-i", in,
		"-map", "0", "-map", "-0:d",
		"-c", "copy",
		"-c:" + v, p.Video.Codec, "-crf", strconv.Itoa(p.Video.CRF), "-preset", p.Video.Preset,
	}
	if p.Video.MaxRateKbps > 0 {
		k := strconv.Itoa(p.Video.MaxRateKbps) + "k"
		args = append(args, "-maxrate:"+v, k, "-bufsize:"+v, strconv.Itoa(2*p.Video.MaxRateKbps)+"k")
	}
	return append(args, "-tag:"+v, p.Video.Tag, out)
}

// Expect is what the new file must look like: the source's streams kept, the main video in the
// Plan's codec (DefaultExpect), and the Plan's minimum saving. packets is the source's main video
// frame count, 0 if unknown. A Plan that scales, converts audio or drops tracks (later slices)
// changes this, not the checks.
func (p Plan) Expect(src media.Info, packets int64) Expect {
	e := DefaultExpect(src, packets)
	if p.Video.Index > 0 { // the encoded stream is not the first video (cover art comes first)
		n := 0
		for k := range e.Streams {
			if e.Streams[k].Type != "video" {
				continue
			}
			switch n {
			case 0:
				e.Streams[k].Codec = src.ByType("video")[0].Codec // copied as it is
			case p.Video.Index:
				e.Streams[k].Codec = "hevc"
			}
			n++
		}
	}
	e.MinSavings = float64(p.MinSavingPct) / 100
	e.MinSSIM = p.MinSSIM
	return e
}

// Snapshot is what a job records when it starts: the Plan, the Expect verification checks the result
// against, and the verification settings in force. It is enough to say afterwards exactly what was
// intended and how the result was judged.
type Snapshot struct {
	Plan   Plan         `json:"plan"`
	Expect Expect       `json:"expect"`
	Verify Verification `json:"verify"`
}

// Verification are the program-wide settings that judge a result (the flags -min-ssim, -decode-check).
type Verification struct {
	MinSSIM          float64 `json:"minSSIM"`          // the threshold actually used
	DecodeCheck      string  `json:"decodeCheck"`      // auto | always
	FullDecodeMaxSec float64 `json:"fullDecodeMaxSec"` // auto: files up to this long are played completely
}

// Fingerprint identifies the settings that decide whether a file is converted, how, and how the
// result is judged (QOL-SAFETY-PLAN.md: codec rule, CRF, preset, thresholds): equal fingerprints, the
// same answer. It leaves out what belongs to the one file (the reason text, its streams, its length).
func (s Snapshot) Fingerprint() string {
	p := s.Plan
	p.Skip, p.Reason = "", ""
	b, _ := json.Marshal(struct {
		Plan                                      Plan
		Verify                                    Verification
		DurationTol, PacketsTol, MinSavings, SSIM float64
	}{p, s.Verify, s.Expect.DurationTol, s.Expect.PacketsTol, s.Expect.MinSavings, s.Expect.MinSSIM})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// compileSlider is a slider position's recipe for one file (TIERS-PLAN.md 2.1): HEVC at the position's
// CRF (one lower up to 720p, one higher at 2160p), medium preset, the source's bit depth, resolution
// and container; every audio track and subtitle copied; the position's minimum saving and SSIM gate.
// Only D5's source formats with enough bits per pixel are converted.
func compileSlider(p Plan, pos SliderPosition, info media.Info) Plan {
	v := info.Video()
	p.Output, p.MinSavingPct, p.MinSSIM = "replace", pos.MinSavingPct, pos.MinSSIM
	p.Eligibility = Eligibility{MinBitsPerPixel: pos.MinBPP}
	if v == nil {
		p.Skip = "it has no video"
		return p
	}
	crf := pos.CRF
	switch {
	case v.Height > 0 && v.Height <= 720:
		crf--
	case v.Height >= 2160 || v.Width >= 3840:
		crf++
	}
	p.Video = VideoEncode{Codec: "libx265", CRF: crf, Preset: SliderPreset, Tag: "hvc1", Index: info.MainVideoOrdinal()}
	if name, ok := alreadyEfficient[v.Codec]; ok {
		p.Skip = name + " is already an efficient format"
		return p
	}
	name, ok := SliderSources[v.Codec]
	if !ok {
		p.Skip = fmt.Sprintf("%s is not a format the slider converts", v.Codec)
		return p
	}
	if why := contentSkip(info); why != "" {
		p.Skip = why
		return p
	}
	if why := subtitlesNotKept(info); why != "" {
		p.Skip = why
		return p
	}
	bpp := bitsPerPixel(info, v)
	if bpp <= 0 {
		p.Skip = "its bitrate, picture size or frame rate is unknown, so its saving cannot be judged"
		return p
	}
	if bpp <= pos.MinBPP {
		p.Skip = fmt.Sprintf("already efficient: %.3f bits per pixel, %s needs more than %.3f", bpp, pos.Name, pos.MinBPP)
		return p
	}
	p.Reason = fmt.Sprintf("%s at %d kbps, %.3f bits per pixel (more than %.3f)", name, info.BitrateKbps, bpp, pos.MinBPP)
	return p
}

// bitsPerPixel is the video's bits per pixel per frame: how much data each pixel gets.
func bitsPerPixel(info media.Info, v *media.Stream) float64 {
	if info.BitrateKbps <= 0 || v.Width <= 0 || v.Height <= 0 || v.FrameRate <= 0 {
		return 0
	}
	return float64(info.BitrateKbps) * 1000 / (float64(v.Width) * float64(v.Height) * v.FrameRate)
}

// contentSkip is where files whose content is not converted are recognised: HDR and Dolby Vision
// (slices 13/14 add them with typed reasons, as conditions every template has). Nothing yet.
func contentSkip(media.Info) string { return "" }

// subtitlesNotKept says why the file's subtitles could not be copied into its own container ("" if
// they can). The container is kept, so only a format the container cannot hold stops it.
func subtitlesNotKept(info media.Info) string {
	holds := map[string]map[string]bool{ // containers with a short list; others hold any subtitle
		"mp4": {"mov_text": true, "dvd_subtitle": true}, "mov": {"mov_text": true, "dvd_subtitle": true},
	}
	ok, limited := holds[info.Container]
	if !limited {
		return ""
	}
	for _, s := range info.Subtitles() {
		if !ok[s.Codec] {
			return fmt.Sprintf("its %s subtitles cannot be kept in %s", s.Codec, info.Container)
		}
	}
	return ""
}
