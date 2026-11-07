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
	FromCodec      string `json:"fromCodec,omitempty"`
	MinBitrateKbps int    `json:"minBitrateKbps,omitempty"`
}

// VideoEncode is how the main video is encoded. Everything else is copied.
type VideoEncode struct {
	Codec  string `json:"codec"` // ffmpeg encoder
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
		d := Default()
		r = Rule{FromCodec: d.FromCodec, MinBitrateKbps: d.MinBitrateKbps, CRF: pos.CRF, Preset: SliderPreset}
		p.Output, p.MinSavingPct = "replace", pos.MinSavingPct
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
	args := []string{
		"-y", "-nostdin", "-v", "error", "-progress", "pipe:1", "-nostats",
		"-i", in,
		"-map", "0", "-map", "-0:d",
		"-c", "copy",
		"-c:v:0", p.Video.Codec, "-crf", strconv.Itoa(p.Video.CRF), "-preset", p.Video.Preset,
	}
	if p.Video.MaxRateKbps > 0 {
		k := strconv.Itoa(p.Video.MaxRateKbps) + "k"
		args = append(args, "-maxrate:v:0", k, "-bufsize:v:0", strconv.Itoa(2*p.Video.MaxRateKbps)+"k")
	}
	return append(args, "-tag:v:0", p.Video.Tag, out)
}

// Expect is what the new file must look like: the source's streams kept, the main video in the
// Plan's codec (DefaultExpect), and the Plan's minimum saving. packets is the source's main video
// frame count, 0 if unknown. A Plan that scales, converts audio or drops tracks (later slices)
// changes this, not the checks.
func (p Plan) Expect(src media.Info, packets int64) Expect {
	e := DefaultExpect(src, packets)
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
