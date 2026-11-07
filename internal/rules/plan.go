// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A Plan is what to do with one file: compiled from its library's flow template and the file's probe
// (TIERS-PLAN.md 1.2). The scanner asks it whether to convert, the worker runs its ffmpeg arguments
// and checks the result against its Expect. A job keeps its Plan as a snapshot, so what was intended
// can be read back later (slice 17's audit log).
type Plan struct {
	Skip            string      `json:"skip,omitempty"`     // why this file is not converted ("" = convert it)
	SkipCode        string      `json:"skipCode,omitempty"` // Skip as a code the API can count (Skip* below)
	Reason          string      `json:"reason,omitempty"`   // why it is converted, shown on the job
	Eligibility     Eligibility `json:"eligibility"`        // the rule that decided Skip (part of the fingerprint)
	Video           VideoEncode `json:"video"`
	Output          string      `json:"output"`                 // replace | beside
	Container       string      `json:"container,omitempty"`    // the new file's type (its extension, "mkv" or "mp4"); "" = the original's
	MinSavingPct    int         `json:"minSavingPct,omitempty"` // the new file must be this much smaller (0: just smaller)
	MinSSIM         float64     `json:"minSSIM,omitempty"`      // picture threshold; 0 = the program's -min-ssim
	Kind            string      `json:"kind"`                   // the template kind it was compiled from
	CompilerVersion int         `json:"compilerVersion"`
}

// Skip codes: why a file is not converted, as the Preview counts them (UI-API-CONTRACT.md, the open
// "skipped" enum, shared with the Skipped list). The Plan's Skip says the same in a sentence for the job record.
const (
	SkipAlreadyEfficient = "already_efficient"    // enough data per pixel already, or below the legacy bitrate
	SkipAlreadyHEVC      = "already_hevc"         // HEVC, AV1, VP9: never converted
	SkipOtherFormat      = "format_not_converted" // a format this setting does not convert
	SkipNoVideo          = "no_video"
	SkipUnknownBitrate   = "unknown_bitrate"    // its saving cannot be judged
	SkipCannotKeepTrack  = "cannot_keep_track"  // a track (its subtitles) cannot stay in its container
	SkipSettingCannotRun = "setting_cannot_run" // the flow cannot run (missing values, a kind not built yet)
	SkipHDR              = "hdr_dv"             // HDR or Dolby Vision: left as it is (D3), unless the flow says otherwise
	SkipUnreadable       = "unreadable"         // the file could not be read (the probe failed); set by callers
)

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
	// Height scales the picture down to this height, keeping its shape (selectors' resolution); 0 = as it is.
	Height int `json:"height,omitempty"`
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
			p.Skip, p.SkipCode = fmt.Sprintf("its library's flow is missing values (%+v)", pr), SkipSettingCannotRun
			return p
		}
		r = Rule{FromCodec: pr.FromCodec, MinBitrateKbps: pr.MinBitrateKbps, CRF: pr.CRF, Preset: pr.Preset}
		p.Output = outputOr(pr.Output)
		// D3, on purpose since slice 25a: an HDR file the rule would convert is left as it is
		// (converting drops its HDR information); everything else decides exactly as before.
		if ok, _ := r.Match(info); ok && info.IsHDR() {
			p.Eligibility = Eligibility{FromCodec: r.FromCodec, MinBitrateKbps: r.MinBitrateKbps}
			p.Video = VideoEncode{Codec: "libx265", CRF: r.CRF, Preset: r.Preset, Tag: "hvc1"}
			p.Skip, p.SkipCode = hdrSkip, SkipHDR
			return p
		}
	case KindSlider:
		pos, ok := SliderAt(t.Params.Quality)
		if !ok {
			p.Skip, p.SkipCode = fmt.Sprintf("slider position %d does not exist", t.Params.Quality), SkipSettingCannotRun
			return p
		}
		return compileSelectors(p, DefaultSelectors(pos.Position), info)
	case KindSelectors:
		if t.Selectors == nil || t.Selectors.Validate() != nil {
			p.Skip, p.SkipCode = "its library's setting has a choice this version cannot run", SkipSettingCannotRun
			return p
		}
		return compileSelectors(p, *t.Selectors, info)
	default:
		p.Skip, p.SkipCode = fmt.Sprintf("flows of kind %q cannot run yet", t.Kind), SkipSettingCannotRun
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
		switch _, modern := alreadyEfficient[info.VideoCodec]; {
		case info.VideoCodec != r.FromCodec && modern:
			p.SkipCode = SkipAlreadyHEVC
		case info.VideoCodec != r.FromCodec:
			p.SkipCode = SkipOtherFormat
		default:
			p.SkipCode = SkipAlreadyEfficient // below the rule's bitrate
		}
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
	if p.Video.Height > 0 {
		args = append(args, "-filter:"+v, "scale=-2:"+strconv.Itoa(p.Video.Height))
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
	if p.Video.Height > 0 { // scaled down: the encoded stream's picture size is the new one
		n := 0
		for k := range e.Streams {
			if e.Streams[k].Type != "video" {
				continue
			}
			if n == p.Video.Index {
				e.Streams[k].Width, e.Streams[k].Height = ScaledWidth(e.Streams[k].Width, e.Streams[k].Height, p.Video.Height), p.Video.Height
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
	p.Skip, p.SkipCode, p.Reason = "", "", ""
	b, _ := json.Marshal(struct {
		Plan                                      Plan
		Verify                                    Verification
		DurationTol, PacketsTol, MinSavings, SSIM float64
	}{p, s.Verify, s.Expect.DurationTol, s.Expect.PacketsTol, s.Expect.MinSavings, s.Expect.MinSSIM})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// compileSelectors is the recipe for one file from the Standard choices; a slider position is its
// DefaultSelectors (TIERS-PLAN.md 2.1): HEVC at the quality's CRF (one lower up to 720p, one higher at
// 2160p, by the new picture size), the Speed's preset, the source's bit depth and (unless capped)
// resolution; every audio track and subtitle copied; the quality's SSIM gate and the minimum saving.
// Only D5's source formats are converted; HDR and Dolby Vision are left alone unless SkipHDR is off,
// and files already small for their picture size unless SkipSmall is off.
func compileSelectors(p Plan, s Selectors, info media.Info) Plan {
	pos, _ := SliderAt(s.Quality)
	v := info.Video()
	p.Output, p.MinSavingPct, p.MinSSIM = s.AfterConverting, s.MinSavingPct, pos.MinSSIM
	if s.FileType != "keep" {
		p.Container = s.FileType
	}
	if s.SkipSmall {
		p.Eligibility = Eligibility{MinBitsPerPixel: pos.MinBPP}
	}
	if v == nil {
		p.Skip, p.SkipCode = "it has no video", SkipNoVideo
		return p
	}
	height := 0
	if limit := resolutions[s.Resolution]; limit > 0 && v.Height > limit {
		height = limit // only ever smaller
	}
	w, h := v.Width, v.Height
	if height > 0 {
		w, h = ScaledWidth(v.Width, v.Height, height), height
	}
	crf := pos.CRF
	switch {
	case h > 0 && h <= 720:
		crf--
	case h >= 2160 || w >= 3840:
		crf++
	}
	p.Video = VideoEncode{Codec: "libx265", CRF: crf, Preset: SpeedPresets[s.Speed], Tag: "hvc1",
		Index: info.MainVideoOrdinal(), MaxRateKbps: maxBitrates[s.MaxBitrate] * 1000, Height: height}
	if name, ok := alreadyEfficient[v.Codec]; ok {
		p.Skip, p.SkipCode = name+" is already an efficient format", SkipAlreadyHEVC
		return p
	}
	name, ok := SliderSources[v.Codec]
	if !ok {
		p.Skip, p.SkipCode = fmt.Sprintf("%s is not a format this setting converts", v.Codec), SkipOtherFormat
		return p
	}
	if s.SkipHDR && info.IsHDR() {
		p.Skip, p.SkipCode = hdrSkip, SkipHDR
		return p
	}
	if why := tracksNotKept(info, p.Container); why != "" {
		p.Skip, p.SkipCode = why, SkipCannotKeepTrack
		return p
	}
	kbps := videoKbps(info, v)
	bpp := bitsPerPixel(kbps, v)
	if !s.SkipSmall {
		p.Reason = fmt.Sprintf("%s at %d kbps", name, kbps)
		return p
	}
	if bpp <= 0 {
		p.Skip, p.SkipCode = "its bitrate, picture size or frame rate is unknown, so its saving cannot be judged", SkipUnknownBitrate
		return p
	}
	if bpp <= pos.MinBPP {
		p.Skip, p.SkipCode = fmt.Sprintf("already efficient: %.3f bits per pixel, %s needs more than %.3f", bpp, pos.Name, pos.MinBPP), SkipAlreadyEfficient
		return p
	}
	p.Reason = fmt.Sprintf("%s at %d kbps, %.3f bits per pixel (more than %.3f)", name, kbps, bpp, pos.MinBPP)
	return p
}

// hdrSkip is why an HDR or Dolby Vision file is left as it is (D3).
const hdrSkip = "it is HDR or Dolby Vision, which is left as it is (converting would lose its HDR information)"

// ScaledWidth is the width ffmpeg's scale=-2:height gives a w x h picture: the same shape, rounded to
// the nearest even number.
func ScaledWidth(w, h, height int) int {
	if w <= 0 || h <= 0 {
		return 0
	}
	return int((int64(height)*int64(w)+int64(h))/(2*int64(h))) * 2
}

// videoKbps is the main video's bitrate: its own stream bitrate. When the stream does not say (many
// mkv files), it falls back on purpose to the whole file's average (size x 8 / length), which
// includes audio and so slightly overstates the video. info.BitrateKbps is not used: it belongs to
// the first video stream, which is the cover art in many media-manager files.
func videoKbps(info media.Info, v *media.Stream) int {
	if v.BitrateKbps > 0 {
		return v.BitrateKbps
	}
	if info.SizeBytes > 0 && info.DurationSec > 0 {
		return int(float64(info.SizeBytes) * 8 / info.DurationSec / 1000)
	}
	if info.MainVideoOrdinal() == 0 { // the first video is the main one, so its headline rate is too
		return info.BitrateKbps
	}
	return 0
}

// SourceVideo is the main video's bitrate and bits per pixel per frame as the slider judges them (0 when
// unknown). The Preview estimates from the same numbers.
func SourceVideo(info media.Info) (kbps int, bpp float64) {
	v := info.Video()
	if v == nil {
		return 0, 0
	}
	kbps = videoKbps(info, v)
	return kbps, bitsPerPixel(kbps, v)
}

// bitsPerPixel is the video's bits per pixel per frame: how much data each pixel gets.
func bitsPerPixel(kbps int, v *media.Stream) float64 {
	if kbps <= 0 || v.Width <= 0 || v.Height <= 0 || v.FrameRate <= 0 {
		return 0
	}
	return float64(kbps) * 1000 / (float64(v.Width) * float64(v.Height) * v.FrameRate)
}

// tracksNotKept says why a track of the file could not be copied into the new file's type ("" if every
// one can): container is the new type ("mkv", "mp4"), "" for the original's own. MP4 and MOV hold only a
// few subtitle and audio formats and no attachments (fonts); MKV holds everything except MP4's own text
// subtitles. ffprobe names a container by its alias list ("mov,mp4,m4a,3gp,3g2,mj2"), so every alias is
// looked at.
func tracksNotKept(info media.Info, container string) string {
	target := container
	if target == "" {
		for _, alias := range strings.Split(info.Container, ",") {
			if a := strings.TrimSpace(alias); a == "mp4" || a == "mov" {
				target = "mp4"
			}
		}
		if target == "" {
			return "" // its own container held them already
		}
	}
	name := map[string]string{"mp4": "MP4", "mkv": "MKV"}[target]
	if container == "" {
		name = info.Container
	}
	switch target {
	case "mp4":
		subs := map[string]bool{"mov_text": true, "dvd_subtitle": true}
		audio := map[string]bool{"aac": true, "ac3": true, "eac3": true, "mp3": true, "alac": true, "opus": true, "flac": true}
		for _, s := range info.Streams {
			switch {
			case s.Type == "subtitle" && !subs[s.Codec]:
				return fmt.Sprintf("its %s subtitles cannot be kept in %s", s.Codec, name)
			case s.Type == "audio" && container != "" && !audio[s.Codec]:
				return fmt.Sprintf("its %s audio cannot be kept in %s", s.Codec, name)
			case s.Type == "attachment" && container != "":
				return fmt.Sprintf("its attached files (such as fonts) cannot be kept in %s", name)
			}
		}
	case "mkv":
		for _, s := range info.Subtitles() {
			if s.Codec == "mov_text" {
				return "its mov_text subtitles cannot be kept in MKV"
			}
		}
	}
	return ""
}
