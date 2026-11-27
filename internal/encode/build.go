// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"fmt"
	"math"
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
		if rung.Family != FamilyCPU {
			return res, fmt.Errorf("%w: the %s family", ErrRungNotBuilt, rung.Family)
		}
		if rung.Decode == DecodeHardware {
			return res, fmt.Errorf("%w: hardware decode", ErrRungNotBuilt)
		}
		want, _ := CPUEncoder(plan.Video.Codec)
		if rung.Encoder != "" && rung.Encoder != want {
			return res, fmt.Errorf("%w: encoder %q cannot encode %s", ErrInvalidPlan, rung.Encoder, plan.Video.Codec)
		}
		encoder = want
	} else {
		if plan.Video.CRF != nil || plan.Video.Preset != "" || plan.Video.Scale != (Scale{}) ||
			plan.Video.FPS != 0 || plan.Video.BitDepth != DepthSource || plan.RateControl != nil {
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

	audioKept, subtitlesKept, notes, err := keptTracks(plan, info)
	if err != nil {
		return res, err
	}
	res.Notes = append(res.Notes, notes...)

	argv := []string{
		"-y", "-nostdin", "-progress", "pipe:1", "-nostats", "-loglevel", "error",
		"-i", info.Path,
		"-map", "0:" + strconv.Itoa(main.Index),
	}
	for _, a := range audioKept {
		argv = append(argv, "-map", "0:"+strconv.Itoa(a.track.Index))
	}
	for _, s := range subtitlesKept {
		argv = append(argv, "-map", "0:"+strconv.Itoa(s.Index))
	}

	if encoding {
		options, err := videoOptions(plan, main, encoder, &res)
		if err != nil {
			return res, err
		}
		argv = append(argv, options...)
	} else {
		argv = append(argv, "-c:v:0", "copy")
	}

	argv = append(argv, "-c:a", "copy", "-c:s", "copy")
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

// keptTracks is the one rule for which input streams reach the output: the plan does not drop
// them, and the container can hold them. Build maps exactly these, and KeptStreams reports them.
// Audio is never dropped for the container's sake (L-AUD-10): audioOutputs converts what it cannot
// hold, and the note says so.
func keptTracks(plan Plan, info media.Info) (audioKept []audioOut, subtitlesKept []media.SubtitleTrack, notes []string, err error) {
	audioKept, audioNotes, err := audioOutputs(plan, info)
	if err != nil {
		return nil, nil, nil, err
	}
	notes = append(notes, audioNotes...)
	subtitleActions, err := namedActions(plan.Subtitles, subtitleIndexes(info.Subtitles))
	if err != nil {
		return nil, nil, nil, err
	}
	for _, s := range info.Subtitles {
		if subtitleActions[s.Index] == StreamDrop {
			continue
		}
		if why := subtitleFits(plan.Container, s.Codec); why != "" {
			notes = append(notes, fmt.Sprintf("dropped subtitle stream #%d (%s): %s", s.Index, s.Codec, why))
			continue
		}
		subtitlesKept = append(subtitlesKept, s)
	}
	return audioKept, subtitlesKept, notes, nil
}

// KeptStreams lists the audio and subtitle stream indexes the output keeps, by ffprobe index.
// The flow engine asks for this list so its result check expects exactly the streams Build mapped.
func KeptStreams(plan Plan, info media.Info) (audio, subtitles []int, err error) {
	a, s, _, err := keptTracks(plan, info)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range a {
		audio = append(audio, t.track.Index)
	}
	for _, t := range s {
		subtitles = append(subtitles, t.Index)
	}
	return audio, subtitles, nil
}

// videoOptions is the video encoder's part of the table. It also appends any HDR note and the
// copy- untouched case is handled by Build.
func videoOptions(plan Plan, main media.VideoTrack, encoder string, res *Result) ([]string, error) {
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
	preset, err := presetName(plan.Video.Codec, plan.Video.Preset)
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
	argv := []string{
		"-c:v:0", encoder,
		"-preset:v:0", preset,
	}
	if plan.RateControl != nil {
		rate, err := rateArgs(plan.Video.Codec, *plan.RateControl)
		if err != nil {
			return nil, err
		}
		argv = append(argv, rate...)
	} else {
		argv = append(argv, "-crf:v:0", strconv.Itoa(crf))
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
	argv = append(argv, "-pix_fmt:v:0", pixelFormat)
	if depth == 10 && plan.Video.Codec == CodecHEVC {
		argv = append(argv, "-profile:v:0", "main10")
	}
	argv = append(argv, "-tag:v:0", tags[plan.Video.Codec])
	if plan.Video.Codec == CodecAV1 {
		argv = append(argv, "-svtav1-params:v:0", "tune=0")
	}
	var filters []string
	if scaleW > 0 && scaleH > 0 {
		filters = append(filters, fmt.Sprintf("scale=%d:%d", scaleW, scaleH))
	}
	if tonemapping {
		filters = append(filters, tonemapChain(transfer, primaries, colorspace, pixelFormat))
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
		if plan.Video.Codec == CodecHEVC && transfer == "smpte2084" {
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

func presetName(codec Codec, choice string) (string, error) {
	if choice == "" {
		choice = "normal"
	}
	name, ok := presets[codec][choice]
	if !ok {
		return "", fmt.Errorf("%w: preset %q is not offered for %s", ErrInvalidPlan, choice, codec)
	}
	return name, nil
}

// namedActions turns the plan's stream list into a map from input index to action. A stream the
// plan does not name is copied; a duplicate or an index the file does not have is a plan error,
// because it means the plan was built against another file.
func namedActions(parts []StreamPlan, exists map[int]bool) (map[int]StreamAction, error) {
	out := make(map[int]StreamAction, len(parts))
	for _, part := range parts {
		if !exists[part.Index] {
			return nil, fmt.Errorf("%w: stream %d is not in the file", ErrInvalidPlan, part.Index)
		}
		if _, dup := out[part.Index]; dup {
			return nil, fmt.Errorf("%w: stream %d is named twice", ErrInvalidPlan, part.Index)
		}
		switch part.Action {
		case StreamCopy, StreamDrop:
		case StreamConvert:
			return nil, fmt.Errorf("%w: converting stream %d", ErrNotBuiltYet, part.Index)
		default:
			return nil, fmt.Errorf("%w: action %q on stream %d", ErrInvalidPlan, part.Action, part.Index)
		}
		out[part.Index] = part.Action
	}
	return out, nil
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

func subtitleFits(container Container, codec string) string {
	switch container {
	case ContainerMP4:
		if codec != "mov_text" {
			return "MP4 cannot hold it"
		}
	case ContainerWebM:
		if codec != "webvtt" {
			return "WebM cannot hold it"
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
