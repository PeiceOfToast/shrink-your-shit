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
	Output          string      `json:"output"`                  // replace | beside
	OutputDir       string      `json:"outputDir,omitempty"`     // "folder" (S6): the new files go here, in the library\'s sub-folders
	Container       string      `json:"container,omitempty"`     // the new file's type (its extension, "mkv" or "mp4"); "" = the original's
	Tracks          []TrackOp   `json:"tracks,omitempty"`        // audio and subtitle tracks dropped or converted (slice 25b); others copied
	AllAudioKept    bool        `json:"allAudioKept,omitempty"`  // "my languages" matched no audio track, so all of it is kept
	MainAudioUsed   bool        `json:"mainAudioUsed,omitempty"` // Basic's stereo AAC matched no track: the main one is kept (S4)
	HDRNote         string      `json:"hdrNote,omitempty"`       // what an HDR conversion does not keep (S3): HDR10+ scene data, Dolby Vision
	AudioFolded     bool        `json:"audioFolded,omitempty"`   // a 7.1 track became 5.1 E-AC-3/AC-3: the job log says FoldedNote (design, 01:52Z)
	SmallKept       bool        `json:"smallKept,omitempty"`     // a small stereo track kept as it is: the job log says SmallKeptNote (design, 02:26Z)
	TypeNote        string      `json:"typeNote,omitempty"`      // "keep" turned this file into MKV: said once in the job log (review 4ac9c0d)
	AudioOrder      []int       `json:"audioOrder,omitempty"`    // the original's audio stream indexes in order, when Tracks has any
	SubtitleOrder   []int       `json:"subtitleOrder,omitempty"` // and its subtitle stream indexes
	MinSSIM         float64     `json:"minSSIM,omitempty"`       // picture threshold; 0 = the program's -min-ssim
	Kind            string      `json:"kind"`                    // the template kind it was compiled from
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
	SkipAlreadyInFormat  = "already_in_format"  // already the chosen video format, file type and audio (Basic S4, Standard S6)
	SkipModernFormat     = "modern_format"      // AV1 or VP9 with an HEVC/H.264 target: converting loses quality, usually grows (S6)
	SkipSettingCannotRun = "setting_cannot_run" // the flow cannot run (missing values, a kind not built yet)
	SkipDV               = "dv"                 // Dolby Vision, left as it is by the flow's setting (S3; includeDolbyVision off)
	SkipDVProfile5       = "dv_profile5"        // Dolby Vision without an HDR10/HLG base (profile 5, or unknown): always left as it is
	// File guards (slice 23, SAF-4), decided from the file itself before any probe (scanner.Guard):
	SkipSharedFile    = "shared_file"    // shared with another folder (more than one name): converting frees no space
	SkipSymlink       = "symlink"        // a link to a file outside the library: never followed
	SkipStillChanging = "still_changing" // changed since the last look (still downloading?): looked at again next scan
	SkipUnreadable    = "unreadable"     // the file could not be read (the probe failed); set by callers
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
	// Copy: the video is copied as it is, not encoded (S4: an HEVC file only repackaged as MKV). The
	// picture comparison is then skipped: the stream is the same bytes.
	Copy bool `json:"copy,omitempty"`
	// HDR: an HDR10 or HLG source is encoded 10-bit with its HDR set from the probe (S3); nil = SDR.
	HDR *HDR `json:"hdr,omitempty"`
	// Format is the video format made (S6: h264, av1, vp9); "" is HEVC, as every Plan before S6.
	Format string `json:"format,omitempty"`
	// Extra are the encoder's own arguments (S6: SVT-AV1's tune, VP9's constant quality and speed).
	Extra []string `json:"extra,omitempty"`
}

// HDR is what an HDR10 or HLG video keeps through the encoder (RESEARCH/hdr-dv.md): the colour
// description and, for HDR10 (PQ), the static metadata, all written explicitly rather than hoped to
// pass through.
type HDR struct {
	Transfer      string `json:"transfer"`  // smpte2084 (PQ, HDR10) | arib-std-b67 (HLG)
	Primaries     string `json:"primaries"` // bt2020
	Matrix        string `json:"matrix"`    // bt2020nc
	MasterDisplay string `json:"masterDisplay,omitempty"`
	CLL           string `json:"cll,omitempty"`
}

// x265Params is libx265's -x265-params for h.
func (h HDR) x265Params() string {
	s := "colorprim=" + h.Primaries + ":transfer=" + h.Transfer + ":colormatrix=" + h.Matrix
	if h.Transfer == "smpte2084" {
		s += ":hdr10=1:hdr10-opt=1"
		if h.MasterDisplay != "" {
			s += ":master-display=" + h.MasterDisplay
		}
		if h.CLL != "" {
			s += ":max-cll=" + h.CLL
		}
	}
	return s
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
	case KindSlider:
		pos, ok := SliderAt(t.Params.Quality)
		if !ok {
			p.Skip, p.SkipCode = fmt.Sprintf("slider position %d does not exist", t.Params.Quality), SkipSettingCannotRun
			return p
		}
		// Basic (S4): the position's selectors, its audio choice, always MKV, replace only
		s := DefaultSelectors(pos.Position)
		s.Audio, s.FileType = BasicAudio(t.Params.Audio), "mkv"
		return compileSelectors(p, s, info, SplitLanguages(t.Languages), true)
	case KindSelectors:
		if t.Selectors == nil {
			p.Skip, p.SkipCode = "its library's setting has a choice this version cannot run", SkipSettingCannotRun
			return p
		}
		s := t.Selectors.Upgraded() // a flow saved before S6 runs as its migration will save it
		if s.Validate() != nil {
			p.Skip, p.SkipCode = "its library's setting has a choice this version cannot run", SkipSettingCannotRun
			return p
		}
		return compileSelectors(p, s, info, SplitLanguages(t.Languages), false)
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
	// S3, for every flow: HDR10 kept, Dolby Vision left as it is. Also on a skipped Plan, which a job
	// queued before its flow changed still runs: its encode keeps the HDR too.
	p.applyHDR(info, false)
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
	}
	if p.Video.Copy { // repackaged: the video as it is (the global -c copy), audio as planned
		args = append(args, p.trackArgs()...)
		args = append(args, out)
		return p.withSubtitleFiles(args, out)
	}
	args = append(args, "-c:"+v, p.Video.Codec, "-crf", strconv.Itoa(p.Video.CRF))
	if p.Video.Preset != "" {
		args = append(args, "-preset", p.Video.Preset)
	}
	args = append(args, p.Video.Extra...)
	if h := p.Video.HDR; h != nil { // 10-bit main10 with the HDR written explicitly (S3)
		args = append(args, "-pix_fmt:"+v, "yuv420p10le", "-profile:"+v, "main10",
			"-color_primaries:"+v, h.Primaries, "-color_trc:"+v, h.Transfer, "-colorspace:"+v, h.Matrix,
			"-x265-params:"+v, h.x265Params())
	}
	if p.Video.Height > 0 {
		args = append(args, "-filter:"+v, "scale=-2:"+strconv.Itoa(p.Video.Height))
	}
	args = append(args, p.trackArgs()...)
	if p.Video.MaxRateKbps > 0 {
		k := strconv.Itoa(p.Video.MaxRateKbps) + "k"
		args = append(args, "-maxrate:"+v, k, "-bufsize:"+v, strconv.Itoa(2*p.Video.MaxRateKbps)+"k")
	}
	if p.Video.Tag != "" {
		args = append(args, "-tag:"+v, p.Video.Tag)
	}
	args = append(args, out)
	return p.withSubtitleFiles(args, out)
}

// withSubtitleFiles adds the extracted subtitles: one more output each, next to the new file (the
// worker installs them).
func (p Plan) withSubtitleFiles(args []string, out string) []string {
	for k, o := range p.SubtitleFiles() {
		args = append(args, "-map", "0:"+strconv.Itoa(o.Index), "-c:s", "srt", StagedSrt(out, k))
	}
	return args
}

// trackArgs are the ffmpeg arguments for the Plan's TrackOps: a dropped track is unmapped (-map -0:N),
// a converted one gets its encoder and bitrate by its place among the new file's audio tracks.
func (p Plan) trackArgs() []string {
	if len(p.Tracks) == 0 {
		return nil
	}
	var args []string
	dropped := map[int]bool{}
	for _, op := range p.Tracks {
		if op.Drop && !op.Add {
			dropped[op.Index] = true
			args = append(args, "-map", "-0:"+strconv.Itoa(op.Index))
		}
	}
	// the k-th audio track kept is a:k in the new file
	k := 0
	for _, idx := range p.AudioOrder {
		if dropped[idx] {
			continue
		}
		for _, op := range p.Tracks {
			if op.Index == idx && op.Encoder != "" && !op.Add {
				a := "a:" + strconv.Itoa(k)
				args = append(args, "-c:"+a, op.Encoder, "-b:"+a, strconv.Itoa(op.Kbps)+"k")
				if op.Channels > 0 { // a downmix (S4's stereo AAC): never more channels than the track has
					args = append(args, "-ac:"+a, strconv.Itoa(op.Channels))
				}
			}
		}
		k++
	}
	// tracks made from another (S6's stereo AAC next to surround): mapped again, after the kept ones
	for _, op := range p.Tracks {
		if op.Add {
			a := "a:" + strconv.Itoa(k)
			args = append(args, "-map", "0:"+strconv.Itoa(op.Index), "-c:"+a, op.Encoder, "-b:"+a, strconv.Itoa(op.Kbps)+"k", "-ac:"+a, strconv.Itoa(op.Channels))
			k++
		}
	}
	// the k-th subtitle kept (not dropped, not saved as a file) is s:k in the new file
	k = 0
	for _, idx := range p.SubtitleOrder {
		if dropped[idx] {
			continue
		}
		for _, op := range p.Tracks {
			if op.Index == idx && op.Encoder != "" {
				args = append(args, "-c:s:"+strconv.Itoa(k), op.Encoder)
			}
		}
		k++
	}
	return args
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
	// A phone video's Display Matrix: ffmpeg encodes the picture upright, so a ±90° file's new picture
	// has width and height swapped (before any scaling, which applies to the upright picture).
	// (Not when the video is copied: it keeps its coded size and its Display Matrix.)
	if vs := src.ByType("video"); !p.Video.Copy && p.Video.Index < len(vs) && vs[p.Video.Index].Quarter() {
		n := 0
		for k := range e.Streams {
			if e.Streams[k].Type != "video" {
				continue
			}
			if n == p.Video.Index {
				e.Streams[k].Width, e.Streams[k].Height = e.Streams[k].Height, e.Streams[k].Width
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
	if f := p.Video.Format; f != "" && !p.Video.Copy { // S6: the encoded stream is in the chosen format
		n := 0
		for k := range e.Streams {
			if e.Streams[k].Type != "video" {
				continue
			}
			if n == p.Video.Index {
				e.Streams[k].Codec = f
			}
			n++
		}
	}
	if p.Video.Copy { // repackaged: the video stays as it was (codec, size); no picture comparison
		e.VideoCopied = true
		vs, n := src.ByType("video"), 0
		for k := range e.Streams {
			if e.Streams[k].Type == "video" && n < len(vs) {
				e.Streams[k].Codec = vs[n].Codec // each video stream as it was
				n++
			}
		}
	}
	if p.Container != "" {
		e.Container = Containers[p.Container]
	}
	if v := src.Video(); v != nil && v.IsHDR() { // S3: the original's HDR must survive, or it is kept
		h := HDRExpect{Transfer: v.ColorTransfer, Primaries: v.ColorPrimaries}
		if h.Transfer == "" && v.HDR10Plus && !v.DolbyVision {
			h.Transfer = "smpte2084"
		}
		if h.Transfer == "smpte2084" || p.Video.Copy { // static metadata is HDR10's (HLG has none to write)
			h.MasteringDisplay, h.CLL = v.MasteringDisplay != "", v.CLL != ""
		}
		e.HDR = &h
	}
	e = p.expectTracks(e)
	e.MinSSIM = p.MinSSIM
	return e
}

// expectTracks applies the TrackOps to e: a dropped audio track is not expected, a converted one is
// expected in its new codec (same channels and language). The k-th audio expectation is the original's
// k-th audio track (DefaultExpect keeps their order).
func (p Plan) expectTracks(e Expect) Expect {
	if len(p.Tracks) == 0 {
		return e
	}
	op := map[int]TrackOp{}
	for _, o := range p.Tracks {
		if !o.Add {
			op[o.Index] = o
		}
	}
	order := map[string][]int{"audio": p.AudioOrder, "subtitle": p.SubtitleOrder}
	seen := map[string]int{}
	var out []StreamExpect
	for _, s := range e.Streams {
		if idx, ok := order[s.Type]; ok && seen[s.Type] < len(idx) {
			o, has := op[idx[seen[s.Type]]]
			seen[s.Type]++
			switch {
			case has && o.Drop:
				continue
			case has && o.Codec != "":
				s.Codec = o.Codec
				if o.Channels > 0 {
					s.Channels = o.Channels
				}
				if s.Type == "subtitle" && o.Codec != "mov_text" { // converted text (mov_text to SRT): its default flag kept too;
					s.CheckDefault = true // MP4 has no default flag of its own (every track reads as default), so not for mov_text
				}
			}
		}
		out = append(out, s)
	}
	for _, o := range p.Tracks { // a track made from another: after the kept audio, in its language
		if o.Add {
			out = append(out, StreamExpect{Type: "audio", Codec: o.Codec, Channels: o.Channels, Language: o.Language})
		}
	}
	e.Streams = out
	return e
}

// remaining is info with the tracks the Plan drops left out, as the new file will have them.
func (p Plan) remaining(info media.Info) media.Info {
	if len(p.Tracks) == 0 {
		return info
	}
	op := map[int]TrackOp{}
	for _, o := range p.Tracks {
		if !o.Add {
			op[o.Index] = o
		}
	}
	out := info
	out.Streams = nil
	for _, s := range info.Streams {
		o, has := op[s.Index]
		switch {
		case has && o.Drop:
			continue
		case has && o.Codec != "":
			s.Codec = o.Codec // converted: the new file holds the new codec
		}
		out.Streams = append(out.Streams, s)
	}
	return out
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
		Plan                          Plan
		Verify                        Verification
		DurationTol, PacketsTol, SSIM float64
	}{p, s.Verify, s.Expect.DurationTol, s.Expect.PacketsTol, s.Expect.MinSSIM})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// compileSelectors is the recipe for one file from the Standard choices; a slider position is its
// DefaultSelectors (TIERS-PLAN.md 2.1): HEVC at the quality's CRF (one lower up to 720p, one higher at
// 2160p, by the new picture size), the Speed's preset, the source's bit depth and (unless capped)
// resolution; every audio track and subtitle copied; the quality's SSIM gate. Only D5's source formats
// are converted; HDR and Dolby Vision are left alone unless SkipHDR is off. Every other file of those
// formats is converted, however little data it has per pixel: always standardize (USER-REVIEW
// 2026-11-07; there is no "already small" skip and no minimum saving any more).
//
// basic (a Basic flow, S4): an HEVC file is not skipped but repackaged (the video copied, the audio as
// chosen, MKV), and skipped as already_in_format only when nothing would change; AV1 and VP9 stay
// skipped as already efficient.
func compileSelectors(p Plan, s Selectors, info media.Info, langs []string, basic bool) Plan {
	pos, _ := SliderAt(s.Quality)
	v := info.Video()
	p.Output, p.MinSSIM = s.AfterConverting, pos.MinSSIM
	if s.AfterConverting == "folder" {
		p.OutputDir = s.OutputDir
	}
	if s.FileType != "keep" {
		p.Container = s.FileType
	}
	if v == nil {
		p.Skip, p.SkipCode = "it has no video", SkipNoVideo
		return p
	}
	// The upright picture decides: a phone video's ±90° Display Matrix is encoded upright by ffmpeg,
	// and the scale filter then works on the upright picture (review 44dbabe).
	uw, uh := v.Width, v.Height
	if v.Quarter() {
		uw, uh = v.Height, v.Width
	}
	height := 0
	if limit := resolutions[s.Resolution]; limit > 0 && uh > limit {
		height = limit // only ever smaller
	}
	w, h := uw, uh
	if height > 0 {
		w, h = ScaledWidth(uw, uh, height), height
	}
	crf := pos.CRF
	switch {
	case h > 0 && h <= 720:
		crf--
	case h >= 2160 || w >= 3840:
		crf++
	}
	target := s.Format
	if target == "" {
		target = "hevc"
	}
	vf := VideoFormats[target]
	p.Video = VideoEncode{Codec: vf.Encoder, CRF: crf + vf.CRFOffset, Preset: vf.Presets[s.Speed], Tag: vf.Tag,
		Index: info.MainVideoOrdinal(), MaxRateKbps: CapKbps(s.MaxBitrate, target, w, h, v, sourceVideoKbps(info, v)), Height: height}
	if target != "hevc" {
		p.Video.Format = target
		p.Video.Extra = append(append([]string(nil), vf.Extra[""]...), vf.Extra[s.Speed]...)
	}
	// Already in the target's video format, at its size and with no cap: the video is copied (Basic's
	// repackaging, S4; Standard too since S6), and nothing at all is done when the file type and audio
	// are already as chosen (already_in_format). AV1 and VP9 are not made into HEVC or H.264: converting
	// them loses quality and usually grows the file (modern_format). Every other modern source is
	// converted to the format the person chose.
	// (Dolby Vision included is never copied: profiles 7/8 must become HDR10, review 6cafc6f.)
	remux := v.Codec == target && height == 0 && p.Video.MaxRateKbps == 0 && !(v.DolbyVision && s.IncludeDolbyVision)
	name, ok := SliderSources[v.Codec]
	switch modern := alreadyEfficient[v.Codec]; {
	case remux:
		p.Video = VideoEncode{Codec: "copy", Index: info.MainVideoOrdinal(), Copy: true}
		name, ok = modern, true
		if name == "" {
			name = vf.Name
		}
	case (v.Codec == "av1" || v.Codec == "vp9") && (target == "hevc" || target == "h264"):
		p.Skip, p.SkipCode = SkipWords[SkipModernFormat], SkipModernFormat
		return p
	case modern != "":
		name, ok = modern, true
	}
	if !ok {
		p.Skip, p.SkipCode = fmt.Sprintf("%s is not a format this setting converts", v.Codec), SkipOtherFormat
		return p
	}
	if !p.applyHDR(info, s.IncludeDolbyVision) {
		return p
	}
	choice, onlyMine, _ := StandardAudio(s.Audio, s.OnlyMyLanguages) // Basic's my_languages/keep_all, and values from before S6
	// "Keep the file type" for a file whose own type can't hold what this flow makes (an MP4 with VP9
	// video or Opus audio): that file becomes MKV (review 6cafc6f; the rename warning covers it).
	if p.Container == "" && strings.Contains(info.Container, "mp4") && (target == "vp9" || s.Audio == "stereo_opus") {
		p.Container = "mkv"
		switch { // 25b's wording ("File type changed to MKV: MP4 files can't hold Opus.")
		case target == "vp9" && s.Audio == "stereo_opus":
			p.TypeNote = "File type changed to MKV: MP4 files can't hold VP9 video or Opus."
		case target == "vp9":
			p.TypeNote = "File type changed to MKV: MP4 files can't hold VP9 video."
		default:
			p.TypeNote = "File type changed to MKV: MP4 files can't hold Opus."
		}
	}
	container := p.Container // the new file's ("": the original's type; ffprobe can't tell WebM from MKV)
	if container == "" && strings.Contains(info.Container, "mp4") {
		container = "mp4"
	}
	p.Tracks, p.AllAudioKept, p.MainAudioUsed, p.SmallKept = planAudio(choice, onlyMine, langs, info, container)
	// "Keep the file type" for a .webm whose new file WebM can't hold (video other than VP9 or AV1, audio
	// other than Opus or Vorbis): MKV, which holds both (backlog after #93).
	if p.Container == "" && info.Ext == ".webm" {
		var cant []string
		if target != "vp9" && target != "av1" {
			cant = append(cant, vf.Name+" video")
		}
		for _, o := range p.Tracks {
			if o.Type == "audio" && !o.Drop && o.Codec != "" && o.Codec != "opus" && o.Codec != "vorbis" {
				cant = append(cant, audioCodecWords(o.Codec))
				break
			}
		}
		if len(cant) > 0 {
			p.Container, p.TypeNote = "mkv", "File type changed to MKV: WebM files can't hold "+strings.Join(cant, " or ")+"."
		}
	}
	for _, o := range p.Tracks { // a converted track folded from more than 5.1
		for _, a := range info.Audio() {
			if a.Index == o.Index && o.Channels == 6 && a.Channels > 6 {
				p.AudioFolded = true
			}
		}
	}
	p.Tracks = append(p.Tracks, planSubtitles(s.Subtitles, langs, info)...)
	p.Tracks = append(p.Tracks, movTextToSRT(p.Tracks, p.Container, info)...)
	p.Tracks = append(p.Tracks, textToMovText(p.Tracks, p.Container, info)...)
	if len(p.Tracks) > 0 {
		for _, a := range info.Audio() {
			p.AudioOrder = append(p.AudioOrder, a.Index)
		}
		for _, t := range info.Subtitles() {
			p.SubtitleOrder = append(p.SubtitleOrder, t.Index)
		}
	}
	if why := tracksNotKept(p.remaining(info), p.Container); why != "" {
		p.Skip, p.SkipCode = why, SkipCannotKeepTrack
		return p
	}
	if remux && (p.Container == "" || inContainer(info, p.Container)) && !p.changesTracks() {
		words := containerWords(info.Container)
		if info.Ext == ".webm" {
			words = "WebM"
		}
		p.Skip, p.SkipCode = "it is already "+vf.Name+" in "+words+" with the audio you chose", SkipAlreadyInFormat
		return p
	}
	p.Reason = fmt.Sprintf("%s at %d kbps", name, videoKbps(info, v))
	return p
}

// What S3 says about HDR (design's sentences; the notes go to the job's log and the Preview's sample).
const (
	dvSkip        = "it is Dolby Vision, left as it is by this flow's setting"
	dv5Skip       = "it is Dolby Vision profile 5, or another kind with no HDR10 picture to keep (or one we can't read): converting it would show wrong colours"
	HDR10PlusNote = "HDR10+ scene data isn't kept; the file is HDR10."
	DVRemovedNote = "Dolby Vision removed, HDR10 kept."
	DVRemovedHLG  = "Dolby Vision removed, HLG kept."
)

// applyHDR decides an HDR source (S3, the user's decision overturning D3): HDR10 and HLG are converted
// to 10-bit HEVC with their colour description and static metadata set from the probe; HDR10+ becomes
// HDR10 (its scene data is not kept). Dolby Vision is left as it is unless includeDV; even then only
// profiles 7 and 8, whose base layer is HDR10 or HLG, are converted (the Dolby Vision layer is
// dropped): profile 5, or a profile that can't be read, would show wrong colours and is always left
// as it is. A copied video (S4's repackaging) keeps its HDR as it is. False when the file is skipped.
func (p *Plan) applyHDR(info media.Info, includeDV bool) bool {
	v := info.Video()
	if v == nil || !v.IsHDR() {
		return true
	}
	skip := func(why, code string) bool {
		if p.Skip == "" { // a skip already decided (legacy: below its bitrate) keeps its reason
			p.Skip, p.SkipCode = why, code
		}
		return false
	}
	tr := v.ColorTransfer
	if words, code := dvHold(v, includeDV); code != "" {
		return skip(words, code)
	}
	if !v.DolbyVision && tr != "smpte2084" && tr != "arib-std-b67" {
		tr = "smpte2084" // HDR10+ without a transfer in the stream: HDR10+ is PQ
	}
	if p.Video.Copy {
		return true
	}
	if p.Video.Codec != "libx265" { // a target other than HEVC (S6) has no HDR path yet
		return skip("it is HDR, which this version keeps only when converting to HEVC", SkipSettingCannotRun)
	}
	h := &HDR{Transfer: tr, Primaries: v.ColorPrimaries, Matrix: v.ColorSpace}
	if h.Primaries == "" {
		h.Primaries = "bt2020"
	}
	if h.Matrix == "" {
		h.Matrix = "bt2020nc"
	}
	if tr == "smpte2084" {
		h.MasterDisplay, h.CLL = v.MasteringDisplay, v.CLL
	}
	p.Video.HDR = h
	switch {
	case v.DolbyVision && tr == "arib-std-b67":
		p.HDRNote = DVRemovedHLG
	case v.DolbyVision:
		p.HDRNote = DVRemovedNote
	case v.HDR10Plus:
		p.HDRNote = HDR10PlusNote
	}
	return true
}

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

// textToMovText converts the text subtitles an MP4 cannot hold as they are (SRT, ASS, WebVTT) into its
// own (mov_text) when the new file is MP4, instead of the whole file being left alone (02:41Z; ASS loses
// its styling). Picture subtitles still can't go in, so a file with them is still skipped with the reason.
// Only tracks no other op drops, saves as a file or converts.
func textToMovText(ops []TrackOp, container string, info media.Info) (out []TrackOp) {
	if container != "mp4" {
		return nil
	}
	has := map[int]bool{}
	for _, o := range ops {
		has[o.Index] = true
	}
	text := map[string]bool{"subrip": true, "ass": true, "ssa": true, "webvtt": true, "text": true}
	for _, s := range info.Subtitles() {
		if text[s.Codec] && !has[s.Index] {
			out = append(out, TrackOp{Index: s.Index, Type: "subtitle", Encoder: "mov_text", Codec: "mov_text"})
		}
	}
	return out
}

// movTextToSRT converts the MP4 text subtitles (mov_text) an MKV cannot hold as they are into SRT text
// inside it (S4, orch-lead 21:19Z): the same text, language and flags, kept in the file. Only when the new
// file is MKV, and only tracks no other op drops, saves as a file or converts.
func movTextToSRT(ops []TrackOp, container string, info media.Info) (out []TrackOp) {
	if container != "mkv" {
		return nil
	}
	has := map[int]bool{}
	for _, o := range ops {
		has[o.Index] = true
	}
	for _, s := range info.Subtitles() {
		if s.Codec == "mov_text" && !has[s.Index] {
			out = append(out, TrackOp{Index: s.Index, Type: "subtitle", Encoder: "srt", Codec: "subrip"})
		}
	}
	return out
}

// changesTracks reports whether the Plan drops or converts any audio or subtitle track.
func (p Plan) changesTracks() bool {
	for _, o := range p.Tracks {
		if o.Drop || o.Encoder != "" || o.ToFile {
			return true
		}
	}
	return false
}

// dvHold says whether video v is Dolby Vision that is left alone (S3): dv when includeDV is off;
// dv_profile5 for profile 5, a profile that can't be read, compatibility id 0, or no HDR10/HLG base in
// the stream (converting those would show wrong colours). "" when v is not Dolby Vision or is converted.
func dvHold(v *media.Stream, includeDV bool) (words, code string) {
	if v == nil || !v.DolbyVision {
		return "", ""
	}
	tr := v.ColorTransfer
	switch {
	case !includeDV:
		return dvSkip, SkipDV
	case v.DVProfile != 7 && v.DVProfile != 8, tr != "smpte2084" && tr != "arib-std-b67",
		v.DVCompat != nil && *v.DVCompat == 0: // no HDR10/HLG base to keep (review a09f6f5)
		return dv5Skip, SkipDVProfile5
	}
	return "", ""
}

// DolbyVisionHold is dvHold for t's flow: whether the file is Dolby Vision this flow leaves alone,
// decided from the probe alone, whatever else the Plan says (review 7ab8f72: an earlier skip such as
// already_hevc, or a legacy rule's, must not let a queued job encode it). Only a Standard flow can
// include Dolby Vision.
func (t Template) DolbyVisionHold(info media.Info) (words, code string) {
	include := t.Kind == KindSelectors && t.Selectors != nil && t.Selectors.IncludeDolbyVision
	return dvHold(info.Video(), include)
}

// sourceVideoKbps is the source video's own bitrate for the "never above the file's own" ceiling: the
// stream's, or the whole file's less its audio (each audio track of unknown bitrate counted at 640
// kbps, so the estimate stays low); 0 when it can't be told (no ceiling) (review 6cafc6f).
func sourceVideoKbps(info media.Info, v *media.Stream) int {
	if v.BitrateKbps > 0 {
		return v.BitrateKbps
	}
	kbps := info.BitrateKbps
	for _, a := range info.Audio() {
		if a.BitrateKbps > 0 {
			kbps -= a.BitrateKbps
		} else {
			kbps -= audioEstimateKbps(a)
		}
	}
	return max(kbps, 0)
}

// audioEstimateKbps is an upper bound on an audio track's bitrate when the probe has none, so the
// video's estimate (the file's bitrate less its audio) is never above the real video rate and the cap
// stays at or below it (review 4ac9c0d, 48f07eb). Too high only lowers the cap. Lossless 640 kbps a
// channel (5.1 about 3.8 Mbps, 7.1 about 5.1), DTS 1536, AC-3 640 (its maximum), E-AC-3 1024 up to
// 5.1 and 1536 for 7.1, other lossy 160 a channel up to 640 (stereo 320: MP3's maximum, AAC's usual).
func audioEstimateKbps(a media.Stream) int {
	ch := a.Channels
	if ch <= 0 {
		ch = 2
	}
	switch {
	case a.Lossless():
		return 640 * ch
	case a.Codec == "dts":
		return 1536
	case a.Codec == "ac3":
		return 640
	case a.Codec == "eac3" && ch > 6:
		return 1536
	case a.Codec == "eac3":
		return 1024
	}
	return min(160*ch, 640)
}

// audioCodecWords is an audio codec as the log names it.
func audioCodecWords(c string) string {
	switch c {
	case "aac":
		return "AAC"
	case "eac3":
		return "E-AC-3"
	case "ac3":
		return "AC-3"
	}
	return strings.ToUpper(c)
}

// inContainer reports whether the file is already of type c (mkv, mp4, webm). ffprobe names MKV and WebM
// alike, so the file's extension tells them apart (review 32a307d); a probe from before Ext counts as MKV.
func inContainer(info media.Info, c string) bool {
	switch c {
	case "webm":
		return info.Ext == ".webm"
	case "mkv":
		return info.Container == Containers["mkv"] && info.Ext != ".webm"
	}
	return info.Container == Containers[c]
}
