// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// statsTags are the per-stream statistics ffmpeg writes when it encodes. In a converted file they
// describe the old encode, so players would show the wrong numbers; Build clears them, in their
// plain and "-eng" forms, on the re-encoded video stream.
var statsTags = []string{
	"BPS", "DURATION", "NUMBER_OF_BYTES", "NUMBER_OF_FRAMES",
	"_STATISTICS_TAGS", "_STATISTICS_WRITING_APP", "_STATISTICS_WRITING_DATE_UTC",
}

// Build turns a plan, the source's media info and one rung into the ffmpeg argument list. It is
// pure: no exec, no file access, no clock. The argument table is the review surface (L-HW-20).
//
// The log level is "error" on purpose: media.Run keeps the last 50 non-empty stderr lines and
// reports the last one on failure (L-HW-14), so the error line must not be buried under ffmpeg's
// informational report.
func Build(plan Plan, info media.Info, rung Rung) (Result, error) {
	var res Result

	if plan.Output == "" {
		return res, fmt.Errorf("%w: the plan has no output path", ErrInvalidPlan)
	}
	if info.Path == "" {
		return res, fmt.Errorf("%w: the media info has no path", ErrInvalidPlan)
	}
	switch plan.Container {
	case ContainerMKV, ContainerMP4, ContainerWebM:
	default:
		return res, fmt.Errorf("%w: container %q is not mkv, mp4 or webm", ErrInvalidPlan, plan.Container)
	}
	if plan.RateControl != nil && plan.RateControl.TwoPass {
		return res, fmt.Errorf("%w: two passes", ErrNotBuiltYet)
	}
	main, ok := mainVideo(info)
	if !ok {
		return res, fmt.Errorf("%w: the file has no video stream", ErrInvalidPlan)
	}

	encoding := plan.Video.Codec != CodecCopy
	var encoder string
	if encoding {
		switch plan.Video.Codec {
		case CodecHEVC, CodecH264, CodecAV1:
		default:
			return res, fmt.Errorf("%w: codec %q is not hevc, h264, av1 or copy", ErrInvalidPlan, plan.Video.Codec)
		}
		switch rung.Decode {
		case "", DecodeSoftware, DecodeHardware:
		default:
			return res, fmt.Errorf("%w: decode %q is not software or hardware", ErrInvalidPlan, rung.Decode)
		}
		switch rung.Family {
		case FamilyCPU:
			if rung.Decode == DecodeHardware {
				return res, fmt.Errorf("%w: hardware decode on the processor", ErrRungNotBuilt)
			}
			want, _ := CPUEncoder(plan.Video.Codec)
			if rung.Encoder != "" && rung.Encoder != want {
				return res, fmt.Errorf("%w: encoder %q cannot encode %s", ErrInvalidPlan, rung.Encoder, plan.Video.Codec)
			}
			encoder = want
		case FamilyQSV, FamilyVAAPI, FamilyNVENC:
			want, ok := CardEncoder(rung.Family, plan.Video.Codec)
			if !ok {
				return res, fmt.Errorf("%w: %s on a %s card", ErrRungNotBuilt, plan.Video.Codec, rung.Family)
			}
			if rung.Encoder != "" && rung.Encoder != want {
				return res, fmt.Errorf("%w: encoder %q cannot encode %s", ErrInvalidPlan, rung.Encoder, plan.Video.Codec)
			}
			if rung.Family == FamilyVAAPI && rung.Device == "" {
				return res, fmt.Errorf("%w: a VA-API rung needs its render node", ErrInvalidPlan)
			}
			encoder = want
			if rung.Decode == DecodeHardware {
				// The full pipeline is a choice for some plans only (L-HW-11); a refusal here
				// falls through to the next rung, so the file still converts on the processor.
				if err := fullPipelineOK(plan, rung); err != nil {
					return res, err
				}
			}
		case FamilyAMF:
			return res, fmt.Errorf("%w: the amf family", ErrRungNotBuilt)
		default:
			return res, fmt.Errorf("%w: the %q family", ErrInvalidPlan, rung.Family)
		}
	} else {
		if plan.Video.CRF != nil || plan.Video.Preset != "" || plan.Video.Quality != 0 ||
			plan.Video.Scale != (Scale{}) || plan.Video.FPS != 0 || plan.Video.BitDepth != DepthSource ||
			plan.RateControl != nil {
			return res, fmt.Errorf("%w: the video is copied, so its size, rate and depth cannot change", ErrInvalidPlan)
		}
	}

	if plan.Container == ContainerWebM {
		if encoding && plan.Video.Codec != CodecAV1 {
			return res, fmt.Errorf("%w: WebM needs AV1 video", ErrInvalidPlan)
		}
		if !encoding && main.Codec != "av1" && main.Codec != "vp9" {
			return res, fmt.Errorf("%w: WebM needs AV1 or VP9 video, the source is %s", ErrInvalidPlan, main.Codec)
		}
	}

	// The streams: the video, then audio and subtitles in input order. Only streams the plan
	// keeps are mapped; data and attachment streams are never mapped.

	for _, v := range info.Video {
		if v.Index == main.Index {
			continue
		}
		if v.AttachedPicture {
			res.Notes = append(res.Notes, fmt.Sprintf("dropped video stream #%d (%s, cover art)", v.Index, v.Codec))
			continue
		}
		res.Notes = append(res.Notes, fmt.Sprintf("dropped video stream #%d (%s): only one video stream is converted", v.Index, v.Codec))
	}

	audioKept, subtitlesKept, extracts, notes, err := keptTracks(plan, info)
	if err != nil {
		return res, err
	}
	res.Notes = append(res.Notes, notes...)
	if len(extracts) > 0 && plan.SidecarDir == "" {
		return res, fmt.Errorf("%w: the plan extracts subtitles but has no sidecar folder", ErrInvalidPlan)
	}

	argv := []string{
		"-y", "-nostdin", "-progress", "pipe:1", "-nostats", "-loglevel", "error",
	}
	if encoding {
		// The card's device, and for the full pipeline its decoder, are input options: they come
		// before the file they decode (L-HW-10).
		setup, decode := rungSetup(rung)
		argv = append(argv, setup...)
		argv = append(argv, decode...)
	}
	argv = append(argv, "-i", info.Path)
	// An extracted subtitle is an output of its own, written before the new file so a name it
	// cannot take stops the job before anything is placed. The new file's own options follow the
	// extracts, so ffmpeg still binds them to the new file.
	for k, x := range extracts {
		argv = append(argv, "-map", "0:"+strconv.Itoa(x.Index), "-c:s", "srt", StagedSidecar(plan.SidecarDir, k))
	}
	argv = append(argv, "-map", "0:"+strconv.Itoa(main.Index))
	for _, a := range audioKept {
		argv = append(argv, "-map", "0:"+strconv.Itoa(a.track.Index))
	}
	for _, s := range subtitlesKept {
		argv = append(argv, "-map", "0:"+strconv.Itoa(s.track.Index))
	}

	if encoding {
		options, err := videoOptions(plan, main, rung, encoder, &res)
		if err != nil {
			return res, err
		}
		argv = append(argv, options...)
	} else {
		argv = append(argv, "-c:v:0", "copy")
	}

	argv = append(argv, "-c:a", "copy", "-c:s", "copy")
	for i, s := range subtitlesKept {
		if s.codec != "" {
			argv = append(argv, "-c:s:"+strconv.Itoa(i), s.codec)
		}
	}
	for i, a := range audioKept {
		argv = append(argv, audioArgs(i, a)...)
	}
	if plan.Container == ContainerMP4 {
		// Faststart moves the index to the front so a player can start before the whole file is
		// read; MKV and WebM have no such flag (L-HW-15).
		argv = append(argv, "-movflags", "+faststart")
	}
	argv = append(argv, plan.Output)

	res.Argv = argv
	return res, nil
}

// fullPipelineOK refuses the full GPU pipeline — the decoder and the encoder both on the card — for
// a plan whose frames must pass through the processor. It is not a job error: the caller falls
// through to the next rung, which decodes in software and still converts the file (L-HW-11).
func fullPipelineOK(plan Plan, rung Rung) error {
	if rung.Family == FamilyNVENC {
		return fmt.Errorf("%w: NVENC is hybrid, it encodes frames the processor decoded (L-HW-17)", ErrRungNotBuilt)
	}
	if plan.Video.FPS > 0 {
		return fmt.Errorf("%w: a frame rate change runs on the processor", ErrRungNotBuilt)
	}
	if plan.Video.HDR == HDRTonemap {
		return fmt.Errorf("%w: tone mapping runs on the processor", ErrRungNotBuilt)
	}
	return nil
}

// rungSetup is the device arguments a card rung needs before its input: the device the card's
// filters and encoder use, and for the full pipeline the card's decoder (L-HW-10). The names are
// the ones the card probe proved (internal/hw).
func rungSetup(rung Rung) (setup, decode []string) {
	switch rung.Family {
	case FamilyQSV:
		setup = []string{"-init_hw_device", "qsv=qsv:hw", "-filter_hw_device", "qsv"}
		if rung.Decode == DecodeHardware {
			// The decoder's default pool is too small to feed the encoder directly: every
			// allocation then fails ("Cannot allocate memory") and ffmpeg drops most of the
			// frames, writing repeated timestamps (proven on gen9 with iHD 24.1). The hybrid row
			// gives its upload the same spare surfaces (L-HW-11).
			decode = []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-extra_hw_frames", "16"}
		}
	case FamilyVAAPI:
		setup = []string{"-init_hw_device", "vaapi=va:" + rung.Device, "-filter_hw_device", "va"}
		if rung.Decode == DecodeHardware {
			decode = []string{"-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi"}
		}
	}
	return setup, decode
}

// subtitleOut is one subtitle's place in the new file: the input stream, and the codec it is
// written as ("" copies it as it is).
type subtitleOut struct {
	track media.SubtitleTrack
	codec string
}

// keptTracks is the one rule for which input streams reach the output: the plan does not drop or
// extract them, and the container can hold them. Build maps exactly these, and KeptStreams reports
// them. Audio is never dropped for the container's sake (L-AUD-10): audioOutputs converts what it
// cannot hold, and the note says so. Subtitles are converted where the file type can hold the
// text, and left out with a count where it cannot (L-AUD-27, L-AUD-28).
func keptTracks(plan Plan, info media.Info) (audioKept []audioOut, subtitlesKept []subtitleOut, extracts []StreamPlan, notes []string, err error) {
	audioKept, audioNotes, err := audioOutputs(plan, info)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	notes = append(notes, audioNotes...)
	subtitlesKept, extracts, subtitleNotes, err := subtitleOutputs(plan, info)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	notes = append(notes, subtitleNotes...)
	return audioKept, subtitlesKept, extracts, notes, nil
}

// subtitleOutputs applies the file type's rule to every subtitle the plan does not drop:
//
//   - MKV holds every subtitle, so each is copied, except mov_text, which becomes SRT text.
//   - MP4 holds mov_text and dvd_subtitle as they are, and turns the text forms it cannot hold
//     into mov_text (styling is lost); anything else is left out.
//   - WebM holds WebVTT only: webvtt is copied, the other text forms become webvtt, and every
//     other kind (picture subtitles, an unknown one such as TTML) is left out, since the muxer
//     refuses the whole file if it is copied.
//
// The notes say what the person cannot see in the new file: styling that went, and how many
// subtitles were left out. Extractions are not part of the new file at all (L-AUD-22's trap): only
// the streams the plan names are written to their own files.
func subtitleOutputs(plan Plan, info media.Info) (kept []subtitleOut, extracts []StreamPlan, notes []string, err error) {
	actions, parts, err := subtitleActions(plan, info)
	if err != nil {
		return nil, nil, nil, err
	}
	styled := 0
	leftOut, pictures := 0, 0
	for _, s := range info.Subtitles {
		switch actions[s.Index] {
		case StreamDrop:
			continue
		case StreamExtract:
			extracts = append(extracts, StreamPlan{Index: s.Index, Action: StreamExtract, Part: parts[s.Index]})
			continue
		}
		codec, fits, lostStyle := subtitleWrite(plan.Container, s)
		if !fits {
			leftOut++
			if s.Kind() == media.SubtitleBitmap {
				pictures++
			}
			continue
		}
		if lostStyle {
			styled++
		}
		kept = append(kept, subtitleOut{track: s, codec: codec})
	}
	if styled > 0 {
		if plan.Container == ContainerWebM {
			notes = append(notes, styledSubsWebMNote)
		} else {
			notes = append(notes, styledSubsNote)
		}
	}
	if leftOut > 0 {
		notes = append(notes, leftOutNote(plan.Container, leftOut, pictures))
	}
	return kept, extracts, notes, nil
}

// subtitleWrite is the file type's answer for one subtitle: the codec it is written as ("" copies
// it), whether the file type can hold it at all, and whether converting it loses ASS styling.
func subtitleWrite(container Container, s media.SubtitleTrack) (codec string, fits, lostStyle bool) {
	switch container {
	case ContainerMKV:
		if s.Codec == "mov_text" {
			return "srt", true, false
		}
		return "", true, false
	case ContainerMP4:
		switch s.Codec {
		case "mov_text", "dvd_subtitle":
			return "", true, false
		case "subrip", "ass", "ssa", "webvtt", "text":
			return "mov_text", true, s.Codec == "ass" || s.Codec == "ssa"
		}
		return "", false, false
	case ContainerWebM:
		// The WebM muxer takes WebVTT subtitles and nothing else: copying any other codec makes
		// ffmpeg fail to write the file, so the track is left out instead (L-AUD-27).
		if s.Kind() == media.SubtitleText {
			if s.Codec == "webvtt" {
				return "", true, false
			}
			return "webvtt", true, s.Codec == "ass" || s.Codec == "ssa"
		}
		return "", false, false
	}
	return "", true, false
}

// The job log's sentences for subtitle conversions the file type forces.
const (
	styledSubsNote     = "Styled subtitles (ASS) became plain text: MP4 can't keep their styling."
	styledSubsWebMNote = "Styled subtitles (ASS) became plain text: WebM can't keep their styling."
)

// leftOutNote says how many subtitles the file type could not hold (page 07, Save). pictures is
// how many of the n left out are picture subtitles: WebM only calls them that when they all are,
// since it also leaves out kinds it does not know (a TTML track).
func leftOutNote(container Container, n, pictures int) string {
	name := "MP4"
	what := "subtitle"
	if container == ContainerWebM {
		name = "WebM"
		if pictures == n {
			what = "picture subtitle"
		}
	}
	if n == 1 {
		return fmt.Sprintf("1 %s was left out: %s can't hold it.", what, name)
	}
	return fmt.Sprintf("%d %ss were left out: %s can't hold them.", n, what, name)
}

// Extracts lists the subtitles the plan writes to their own files, in the order their files are
// written (the order of the streams in the file), each with its name part.
func Extracts(plan Plan, info media.Info) ([]StreamPlan, error) {
	_, extracts, _, err := subtitleOutputs(plan, info)
	return extracts, err
}

// SrtName is the name an extracted subtitle gets beside the new file final ("Film.mkv" with part
// "eng" is "Film.eng.srt"; with no part it is "Film.srt", L-AUD-22).
func SrtName(final, part string) string {
	base := strings.TrimSuffix(final, filepath.Ext(final))
	if part == "" {
		return base + ".srt"
	}
	return base + "." + part + ".srt"
}

// StagedSidecar is where ffmpeg writes the k-th extracted subtitle: a .srt in the staging folder,
// named for its place in the plan (L-AUD-25).
func StagedSidecar(dir string, k int) string {
	return filepath.Join(dir, "sub."+strconv.Itoa(k)+".srt")
}

// KeptStreams lists the audio and subtitle stream indexes the new file keeps, by ffprobe index.
// The flow engine asks for this list so its result check expects exactly the streams Build mapped.
func KeptStreams(plan Plan, info media.Info) (audio, subtitles []int, err error) {
	a, s, _, _, err := keptTracks(plan, info)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range a {
		audio = append(audio, t.track.Index)
	}
	for _, t := range s {
		subtitles = append(subtitles, t.track.Index)
	}
	return audio, subtitles, nil
}

// videoOptions is the video encoder's part of the table, per family (L-HW-10/11/13). It also
// appends any note; the copy case is handled by Build.
func videoOptions(plan Plan, main media.VideoTrack, rung Rung, encoder string, res *Result) ([]string, error) {
	family := rung.Family
	var crf int
	if plan.RateControl != nil && plan.Video.CRF != nil {
		return nil, fmt.Errorf("%w: a bitrate replaces the CRF", ErrInvalidPlan)
	}
	if plan.RateControl == nil {
		if plan.Video.CRF == nil {
			return nil, fmt.Errorf("%w: the video has no CRF", ErrInvalidPlan)
		}
		crf = *plan.Video.CRF
		if crf < 0 || crf > 63 {
			return nil, fmt.Errorf("%w: CRF %d is outside 0..63", ErrInvalidPlan, crf)
		}
	}
	preset, err := presetName(plan.Video.Codec, family, plan.Video.Preset)
	if err != nil {
		return nil, err
	}

	// Dolby Vision: profile 5 has no HDR10 base to fall back to and is left alone; 7 and 8
	// convert as their HDR10 base (L-HW-16).
	if main.HDR.DolbyVision {
		switch main.HDR.DVProfile {
		case 7, 8:
			res.Notes = append(res.Notes, fmt.Sprintf("Dolby Vision profile %d is written as its HDR10 base", main.HDR.DVProfile))
		default:
			return nil, fmt.Errorf("%w: Dolby Vision profile %d has no HDR10 base to convert", ErrSkip, main.HDR.DVProfile)
		}
	}
	if plan.Video.HDR == HDRSkip && main.HDRKind() != "SDR" {
		return nil, fmt.Errorf("%w: %s is left alone by this flow", ErrSkip, main.HDRKind())
	}

	// The source's transfer. A Dolby Vision stream whose transfer line is missing is still HDR, as
	// its HDR10 base.
	transfer := main.ColorTransfer
	isHDR := transfer == "smpte2084" || transfer == "arib-std-b67"
	if !isHDR && main.HDR.DolbyVision {
		isHDR = true
		transfer = "smpte2084"
	}
	primaries := main.ColorPrimaries
	if primaries == "" {
		primaries = "bt2020"
	}
	colorspace := main.ColorSpace
	if colorspace == "" {
		colorspace = "bt2020nc"
	}
	// tonemap converts an HDR picture to SDR BT.709 (L-HW-16); keep writes the HDR metadata through.
	tonemapping := plan.Video.HDR == HDRTonemap && isHDR
	keepHDR := isHDR && plan.Video.HDR != HDRSkip && !tonemapping

	depth := 8
	switch plan.Video.BitDepth {
	case DepthSource:
		if main.BitDepth >= 10 {
			depth = 10
		}
	case Depth8:
		depth = 8
	case Depth10:
		depth = 10
	default:
		return nil, fmt.Errorf("%w: bit depth %d is not source, 8 or 10", ErrInvalidPlan, plan.Video.BitDepth)
	}
	if keepHDR {
		// Ten-bit output on an HDR source is a fact, not a setting (L-HW-16).
		depth = 10
	}
	if tonemapping && plan.Video.BitDepth == DepthSource {
		// A tone-mapped picture is SDR: 8-bit unless the person asked for ten.
		depth = 8
	}
	if plan.Video.Codec == CodecH264 {
		// H.264 is 8-bit by choice (10-bit and HDR H.264 play on few devices): no ten-bit H.264 is offered, and HDR cannot be kept in it. A plan that asks
		// for either is refused; a ten-bit SDR source is written as 8-bit, and the log says so (#855).
		if keepHDR {
			return nil, fmt.Errorf("%w: H.264 is not used for HDR", ErrInvalidPlan)
		}
		if plan.Video.BitDepth == Depth10 {
			return nil, fmt.Errorf("%w: 10-bit H.264 is not offered", ErrInvalidPlan)
		}
		if depth == 10 {
			depth = 8
			res.Notes = append(res.Notes, "H.264 is written as 8-bit: 10-bit H.264 plays on few devices")
		}
	}

	if rung.Decode == DecodeHardware {
		srcDepth := 8
		if main.BitDepth >= 10 {
			srcDepth = 10
		}
		if depth != srcDepth {
			// The decoder's frames are already the source's depth; changing it needs a processor
			// filter, which the full pipeline does not run (L-HW-11).
			return nil, fmt.Errorf("%w: the picture's depth cannot change on the card", ErrRungNotBuilt)
		}
	}
	if plan.Video.FPS < 0 || math.IsNaN(plan.Video.FPS) || math.IsInf(plan.Video.FPS, 0) {
		return nil, fmt.Errorf("%w: frame rate %v", ErrInvalidPlan, plan.Video.FPS)
	}
	scaleW, scaleH, err := scaleTo(main, plan.Video.Scale)
	if err != nil {
		return nil, err
	}

	pixelFormat := "yuv420p"
	if depth == 10 {
		pixelFormat = "yuv420p10le"
	}
	argv := []string{"-c:v:0", encoder}
	if family == FamilyVAAPI {
		// VA-API's encoders have no speed setting: the driver picks its own pace (L-HW-13). A
		// choice the person made is said out loud rather than silently dropped.
		if plan.Video.Preset != "" && plan.Video.Preset != "normal" {
			res.Notes = append(res.Notes, "The card's VA-API encoder has no speed setting, so this file encodes at the driver's own pace")
		}
	} else {
		argv = append(argv, "-preset:v:0", preset)
	}
	if plan.RateControl != nil {
		if family != FamilyCPU {
			// The card families' bitrate flags are not part of this stage; refusing keeps a number
			// the person chose from being ignored (L-HW-10 is quality-only for now).
			return nil, fmt.Errorf("%w: a bitrate on a %s card", ErrNotBuiltYet, family)
		}
		rate, err := rateArgs(plan.Video.Codec, *plan.RateControl)
		if err != nil {
			return nil, err
		}
		argv = append(argv, rate...)
	} else {
		if plan.Video.Quality < 0 || plan.Video.Quality > 5 {
			return nil, fmt.Errorf("%w: quality %d is not a slider position 1..5", ErrInvalidPlan, plan.Video.Quality)
		}
		argv = append(argv, qualityArgs(family, plan.Video.Codec, crf, plan.Video.Quality)...)
	}
	// x265 takes its own options through one -x265-params flag, so HDR's color description and
	// strict CBR share it rather than one silently replacing the other. libx265's minrate/maxrate
	// alone let a simple picture undershoot far below the average (a static clip encoded at 13
	// kbps against a 1 Mb/s target); hrd and strict-cbr make it pad to the rate. The option is not
	// named nal-hrd=cbr: this libx265 rejects that name.
	var x265 []string
	if plan.RateControl != nil && plan.RateControl.Mode == "cbr" && plan.Video.Codec == CodecHEVC {
		x265 = append(x265, "hrd=1:strict-cbr=1")
	}
	// The card families set their pixel format on the frames themselves: the upload filter's
	// format for QSV and VA-API, the decoder's own for the full pipeline. NVENC takes the
	// processor's frames and names p010le for ten bits (L-HW-13).
	switch family {
	case FamilyCPU:
		argv = append(argv, "-pix_fmt:v:0", pixelFormat)
	case FamilyNVENC:
		argv = append(argv, "-pix_fmt:v:0", nvencFormat(depth))
	}
	if depth == 10 && plan.Video.Codec == CodecHEVC {
		argv = append(argv, "-profile:v:0", "main10")
	}
	argv = append(argv, "-tag:v:0", tags[plan.Video.Codec])
	if family == FamilyCPU && plan.Video.Codec == CodecAV1 {
		// SVT-AV1's own tuning; a card's AV1 encoder has no such parameter (L-HW-13).
		argv = append(argv, "-svtav1-params:v:0", "tune=0")
	}
	var filters []string
	switch family {
	case FamilyCPU, FamilyNVENC:
		// These read the processor's ordinary frames.
		if scaleW > 0 && scaleH > 0 {
			filters = append(filters, fmt.Sprintf("scale=%d:%d", scaleW, scaleH))
		}
		if tonemapping {
			filters = append(filters, tonemapChain(transfer, primaries, colorspace, pixelFormat))
		}
	case FamilyQSV:
		if rung.Decode == DecodeHardware {
			// The full pipeline: the decoder's frames stay on the card, so only a card filter may
			// touch them and nothing is uploaded (L-HW-11).
			if scaleW > 0 && scaleH > 0 {
				filters = append(filters, qsvScale(scaleH))
			}
		} else {
			format := uploadFormat(depth)
			if tonemapping {
				filters = append(filters, tonemapChain(transfer, primaries, colorspace, format))
			} else {
				filters = append(filters, "format="+format)
			}
			filters = append(filters, "hwupload=extra_hw_frames=64")
			if scaleW > 0 && scaleH > 0 {
				filters = append(filters, qsvScale(scaleH))
			}
		}
	case FamilyVAAPI:
		if rung.Decode == DecodeHardware {
			if scaleW > 0 && scaleH > 0 {
				filters = append(filters, vaapiScale(scaleH, uploadFormat(depth)))
			}
		} else {
			if scaleW > 0 && scaleH > 0 {
				filters = append(filters, fmt.Sprintf("scale=%d:%d", scaleW, scaleH))
			}
			format := uploadFormat(depth)
			if tonemapping {
				filters = append(filters, tonemapChain(transfer, primaries, colorspace, format))
			} else {
				filters = append(filters, "format="+format)
			}
			// The upload comes last, after every processor filter (L-HW-11). The plan has no crop
			// and no pixel-shape change, so there is no earlier step to place.
			filters = append(filters, "hwupload")
		}
	}
	if len(filters) > 0 {
		argv = append(argv, "-vf:v:0", strings.Join(filters, ","))
	}
	if plan.Video.FPS > 0 {
		argv = append(argv, "-r:v:0", frameRate(plan.Video.FPS))
	}
	if tonemapping {
		argv = append(argv, "-color_primaries:v:0", "bt709", "-color_trc:v:0", "bt709", "-colorspace:v:0", "bt709")
	}
	if keepHDR {
		argv = append(argv,
			"-color_primaries:v:0", primaries,
			"-color_trc:v:0", transfer,
			"-colorspace:v:0", colorspace,
		)
		if family == FamilyCPU && plan.Video.Codec == CodecHEVC && transfer == "smpte2084" {
			// HDR10 static metadata; HDR10+ dynamic metadata is not carried and becomes HDR10.
			x265 = append(x265,
				"colorprim="+primaries+":transfer=smpte2084:colormatrix="+colorspace+":hdr10=1:hdr10-opt=1")
		}
	}
	if len(x265) > 0 {
		argv = append(argv, "-x265-params:v:0", strings.Join(x265, ":"))
	}
	for _, key := range metadataKeys(plan.MetadataClear) {
		argv = append(argv, "-metadata:s:v:0", key+"=")
	}
	return argv, nil
}

// rateArgs is page 07's bitrate part of the table. The average is the encoder's target; the
// at-most bounds it, with a two-second buffer so a hard scene can spend and the next easy one pays
// it back. CBR keeps the at-most equal to the average (a constant rate as near as the encoder can
// keep it); VBR lets the picture rise to the plan's at-most.
func rateArgs(codec Codec, rc RateControl) ([]string, error) {
	switch rc.Mode {
	case "vbr":
		// A format the ladder leaves out of vbrFormats takes constant bitrate or a CRF instead.
		if codec != CodecHEVC && codec != CodecH264 {
			return nil, fmt.Errorf("%w: variable bitrate is not offered for %s", ErrInvalidPlan, codec)
		}
		if rc.MaxMbps <= 0 {
			return nil, fmt.Errorf("%w: variable bitrate needs an at-most", ErrInvalidPlan)
		}
		if rc.MaxMbps < rc.AvgMbps {
			return nil, fmt.Errorf("%w: the at-most %v Mb/s is below the average %v Mb/s", ErrInvalidPlan, rc.MaxMbps, rc.AvgMbps)
		}
	case "cbr":
	default:
		return nil, fmt.Errorf("%w: rate control %q is not vbr or cbr", ErrInvalidPlan, rc.Mode)
	}
	if rc.AvgMbps <= 0 || math.IsNaN(rc.AvgMbps) || math.IsInf(rc.AvgMbps, 0) ||
		math.IsNaN(rc.MaxMbps) || math.IsInf(rc.MaxMbps, 0) {
		return nil, fmt.Errorf("%w: the bitrate is %v Mb/s", ErrInvalidPlan, rc.AvgMbps)
	}
	avg := kbpsArg(rc.AvgMbps)
	if rc.Mode == "cbr" {
		return []string{
			"-b:v:0", avg,
			"-minrate:v:0", avg,
			"-maxrate:v:0", avg,
			"-bufsize:v:0", kbpsArg(rc.AvgMbps * 2),
		}, nil
	}
	return []string{
		"-b:v:0", avg,
		"-maxrate:v:0", kbpsArg(rc.MaxMbps),
		"-bufsize:v:0", kbpsArg(rc.MaxMbps * 2),
	}, nil
}

// kbpsArg writes a bitrate in the units ffmpeg takes: whole kbps, e.g. 3600k.
func kbpsArg(mbps float64) string {
	return strconv.FormatInt(int64(math.Round(mbps*1000)), 10) + "k"
}

// qualityArgs is the quality flag each encoder takes for the same CRF (L-HW-10), with L-HW-12's
// measured QSV HEVC offset applied at the slider position the CRF came from.
func qualityArgs(family Family, codec Codec, crf, quality int) []string {
	n := crf + cardQualityOffset(family, codec, quality)
	switch family {
	case FamilyQSV:
		return []string{"-global_quality:v:0", strconv.Itoa(n)}
	case FamilyVAAPI:
		return []string{"-rc_mode:v:0", "CQP", "-qp:v:0", strconv.Itoa(n)}
	case FamilyNVENC:
		return []string{"-rc:v:0", "vbr", "-cq:v:0", strconv.Itoa(n)}
	}
	return []string{"-crf:v:0", strconv.Itoa(n)}
}

// uploadFormat is the pixel format a card's upload filter names for the output's depth: p010 for
// ten bits (NV12's ten-bit form), nv12 for eight (L-HW-10).
func uploadFormat(depth int) string {
	if depth == 10 {
		return "p010"
	}
	return "nv12"
}

// nvencFormat is NVENC's software-frame pixel format: p010le for ten bits, yuv420p for eight
// (L-HW-13).
func nvencFormat(depth int) string {
	if depth == 10 {
		return "p010le"
	}
	return "yuv420p"
}

// qsvScale is the card's scaler, computing the width in the filter so it matches the planned even
// width for the upright picture (L-HW-10, L-HW-11: divide late, round once). scale_qsv has no
// "-2 keeps the shape" width, hence the expression.
func qsvScale(height int) string {
	return fmt.Sprintf("scale_qsv=w=trunc(%d*iw/ih/2+0.5)*2:h=%d", height, height)
}

// vaapiScale is the card's scaler for the full pipeline: the width follows the height (-2, even),
// and the format is the output depth's own (L-HW-10).
func vaapiScale(height int, format string) string {
	return fmt.Sprintf("scale_vaapi=w=-2:h=%d:format=%s", height, format)
}

// OutputHeight is the upright height of the picture the plan writes: the source's own when the
// box is empty or would not shrink it, else the height scaleTo fits inside the box. The flow uses
// it to pick the ladder row the output belongs to, so a 4K source scaled to 1080 reads the 1080
// numbers. Build checks the box itself, in scaleTo.
func OutputHeight(v media.VideoTrack, box Scale) int {
	if box.Width > 0 || box.Height > 0 {
		if _, h, err := scaleTo(v, box); err == nil && h > 0 {
			return h
		}
	}
	if v.Rotation%180 != 0 {
		return v.Width
	}
	return v.Height
}

// OutputSize is the picture the plan writes, in the terms the probe reports it: the even size
// scaleTo fits inside the box when the plan scales (ffmpeg turns the picture upright before the
// filter, so the literal is the upright size and the output carries no matrix), else the source's
// own coded size, which an unscaled output keeps along with its rotation. The result check compares
// the new file's probe with it, allowing an encoder's alignment padding.
func OutputSize(v media.VideoTrack, box Scale) (int, int) {
	if w, h, err := scaleTo(v, box); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return v.Width, v.Height
}

// metadataKeys is the video tags to clear: the plan's own, then the statistics tags ffmpeg would
// otherwise carry over from the source, each also in its "-eng" form.
func metadataKeys(planned []string) []string {
	seen := make(map[string]bool)
	var keys []string
	add := func(key string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, key := range planned {
		add(strings.TrimSpace(key))
	}
	for _, key := range statsTags {
		add(key)
		add(key + "-eng")
	}
	return keys
}

// scaleTo fits the picture inside the box keeping its shape, and reports the even output size.
// Zero means nothing to do: no box, or a box that would not make the picture smaller.
func scaleTo(v media.VideoTrack, box Scale) (int, int, error) {
	if box.Width < 0 || box.Height < 0 {
		return 0, 0, fmt.Errorf("%w: scale %dx%d", ErrInvalidPlan, box.Width, box.Height)
	}
	if box.Width == 0 && box.Height == 0 {
		return 0, 0, nil
	}
	if v.Width <= 0 || v.Height <= 0 {
		return 0, 0, fmt.Errorf("%w: the source has no picture size", ErrInvalidPlan)
	}
	// ffmpeg applies the Display Matrix rotation before the filter chain, so the picture `scale`
	// sees is the upright one: a quarter turn swaps the coded sides (review 44dbabe). The output
	// literal is therefore the upright size, which is also what a player will show.
	srcW, srcH := v.Width, v.Height
	if v.Rotation%180 != 0 {
		srcW, srcH = v.Height, v.Width
	}
	width, height := box.Width, box.Height
	if width == 0 {
		width = srcW
	}
	if height == 0 {
		height = srcH
	}
	factor := math.Min(math.Min(float64(width)/float64(srcW), float64(height)/float64(srcH)), 1)
	w := evenRound(float64(srcW) * factor)
	h := evenRound(float64(srcH) * factor)
	if w >= srcW && h >= srcH {
		return 0, 0, nil
	}
	return w, h, nil
}

// evenRound rounds to the nearest even number, never below 2: a 4:2:0 picture needs even sides.
func evenRound(f float64) int {
	n := int(math.Round(f))
	n &^= 1
	if n < 2 {
		n = 2
	}
	return n
}

// tonemapChain is the processor's HDR-to-SDR conversion (L-HW-16): the picture is read as the
// source's transfer and primaries, made linear at 100 nits, converted to BT.709 primaries, tone
// mapped with Hable, and written as BT.709 video range in the output's pixel format. The linear step
// is float, so the tone curve does not band.
func tonemapChain(transfer, primaries, colorspace, pixelFormat string) string {
	// The source's HDR side data is deleted before anything else: libx265 writes frame mastering-display and
	// content-light side data into the stream's codec header, so left in place it would describe an SDR picture as
	// HDR (#855). A stream-level bitstream filter cannot reach that header; the frame filter can.
	return "sidedata=mode=delete:type=MASTERING_DISPLAY_METADATA" +
		",sidedata=mode=delete:type=CONTENT_LIGHT_LEVEL" +
		",sidedata=mode=delete:type=DYNAMIC_HDR_PLUS" +
		",zscale=tin=" + transfer + ":pin=" + primaries + ":min=" + colorspace + ":t=linear:npl=100" +
		",format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0" +
		",zscale=t=bt709:m=bt709:r=tv,format=" + pixelFormat
}

func frameRate(f float64) string {
	if f == math.Trunc(f) {
		return strconv.Itoa(int(f))
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func presetName(codec Codec, family Family, choice string) (string, error) {
	if choice == "" {
		choice = "normal"
	}
	names := presets[codec]
	if family == FamilyQSV || family == FamilyNVENC {
		names = cardPresets
	}
	name, ok := names[choice]
	if !ok {
		return "", fmt.Errorf("%w: preset %q is not offered for %s", ErrInvalidPlan, choice, codec)
	}
	return name, nil
}

// subtitleActions turns the plan's subtitle list into a map from input index to action, and the
// part each extracted one is named with. A stream the plan does not name is copied; a duplicate,
// an index the file does not have, an extraction of something that is not text, or a name part on
// another action is a plan error, because it means the plan was built against another file.
func subtitleActions(plan Plan, info media.Info) (map[int]StreamAction, map[int]string, error) {
	exists := subtitleIndexes(info.Subtitles)
	kinds := make(map[int]media.SubtitleKind, len(info.Subtitles))
	for _, s := range info.Subtitles {
		kinds[s.Index] = s.Kind()
	}
	out := make(map[int]StreamAction, len(plan.Subtitles))
	parts := make(map[int]string)
	for _, part := range plan.Subtitles {
		if !exists[part.Index] {
			return nil, nil, fmt.Errorf("%w: stream %d is not in the file", ErrInvalidPlan, part.Index)
		}
		if _, dup := out[part.Index]; dup {
			return nil, nil, fmt.Errorf("%w: stream %d is named twice", ErrInvalidPlan, part.Index)
		}
		switch part.Action {
		case StreamCopy, StreamDrop:
			if part.Part != "" {
				return nil, nil, fmt.Errorf("%w: stream %d has a name part but is not extracted", ErrInvalidPlan, part.Index)
			}
		case StreamExtract:
			if kinds[part.Index] != media.SubtitleText {
				return nil, nil, fmt.Errorf("%w: stream %d is not a text subtitle, so it cannot be saved as an .srt file", ErrInvalidPlan, part.Index)
			}
			parts[part.Index] = part.Part
		case StreamConvert:
			return nil, nil, fmt.Errorf("%w: converting stream %d", ErrNotBuiltYet, part.Index)
		default:
			return nil, nil, fmt.Errorf("%w: action %q on stream %d", ErrInvalidPlan, part.Action, part.Index)
		}
		out[part.Index] = part.Action
	}
	return out, parts, nil
}

func audioIndexes(tracks []media.AudioTrack) map[int]bool {
	out := make(map[int]bool, len(tracks))
	for _, t := range tracks {
		out[t.Index] = true
	}
	return out
}

func subtitleIndexes(tracks []media.SubtitleTrack) map[int]bool {
	out := make(map[int]bool, len(tracks))
	for _, t := range tracks {
		out[t.Index] = true
	}
	return out
}

// audioFits answers with the reason the container cannot hold the stream, or "" when it can
// (L-HW-15).
func audioFits(container Container, codec string) string {
	switch container {
	case ContainerMP4:
		if codec == "opus" {
			return "MP4 cannot hold Opus"
		}
	case ContainerWebM:
		if codec != "opus" && codec != "vorbis" {
			return "WebM needs Opus or Vorbis audio"
		}
	}
	return ""
}

func mainVideo(info media.Info) (media.VideoTrack, bool) {
	for _, v := range info.Video {
		if !v.AttachedPicture {
			return v, true
		}
	}
	return media.VideoTrack{}, false
}
