// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"errors"
	"strconv"
)

// The four ways Build can refuse or stop. Callers use errors.Is, and the message is a sentence
// for the job log.
var (
	// ErrRungNotBuilt means the rung asks for an encoder family (or a hardware decode) this
	// stage does not build yet.
	ErrRungNotBuilt = errors.New("the encoder family is not built yet")
	// ErrNotBuiltYet means the plan uses a field whose encoder support comes in a later stage.
	// It is refused rather than half-built, so a number the person chose is never silently
	// ignored.
	ErrNotBuiltYet = errors.New("this part of the plan is not built yet")
	// ErrSkip means the file should be left alone rather than converted. It is the backstop
	// behind the step's own decision.
	ErrSkip = errors.New("leave the file alone")
	// ErrInvalidPlan means the plan cannot become a command: a missing output, a codec the
	// container cannot hold, a value outside its range, an unknown preset.
	ErrInvalidPlan = errors.New("the plan is not valid")
)

// Codec is the output video codec. CodecCopy carries the source video through untouched.
type Codec string

const (
	CodecHEVC Codec = "hevc"
	CodecH264 Codec = "h264"
	CodecAV1  Codec = "av1"
	CodecCopy Codec = "copy"
)

// Container is the output container (L-HW-15).
type Container string

const (
	ContainerMKV  Container = "mkv"
	ContainerMP4  Container = "mp4"
	ContainerWebM Container = "webm"
)

// BitDepth is the output bit depth. DepthSource keeps the source's — a ten-bit SDR source stays
// ten-bit — while Depth8 and Depth10 ask for one. HDR that is kept forces ten-bit whatever this
// says (L-HW-16).
type BitDepth int

const (
	DepthSource BitDepth = 0
	Depth8      BitDepth = 8
	Depth10     BitDepth = 10
)

// HDRMode is page 07's HDR choice. The zero value keeps the source's metadata; skip is the step's
// "leave HDR files alone" and reaches Build only as the backstop, where an HDR source is refused;
// tonemap converts an HDR picture to SDR BT.709 (zscale and tonemap on the processor).
type HDRMode string

const (
	HDRKeep    HDRMode = ""
	HDRTonemap HDRMode = "tonemap"
	HDRSkip    HDRMode = "skip"
)

// Scale is the box the picture is fitted inside, keeping its shape and never growing it. A zero
// width or height leaves that side free; both zero means no scaling.
type Scale struct {
	Width, Height int
}

// StreamAction is what happens to one input stream.
type StreamAction string

const (
	// StreamCopy carries the stream through untouched. It is the default for a stream the plan
	// does not name.
	StreamCopy StreamAction = ""
	StreamDrop StreamAction = "drop"
	// StreamConvert re-encodes a stream: the audio step's formats and channels.
	StreamConvert StreamAction = "convert"
	// StreamExtract writes a text subtitle to its own .srt file beside the new file instead of
	// carrying it inside (L-AUD-22).
	StreamExtract StreamAction = "extract"
)

// StreamPlan names an input stream by its ffprobe index and what to do with it. A stream the plan
// does not name is copied.
type StreamPlan struct {
	Index  int
	Action StreamAction
	// Part is the name an extracted subtitle gets, lang[.forced|.sdh][.N] (L-AUD-22); empty means
	// the new file's base name alone. It is unused for every other action.
	Part string
}

// VideoPlan is the video stream part of the plan.
type VideoPlan struct {
	Codec Codec
	// CRF is the quality number the flow computed. It is required when the video is encoded at a
	// quality (Plan.RateControl unset) and ignored when it is copied.
	CRF *int
	// Preset is the named choice "faster", "normal" or "slower"; empty means normal. Build maps
	// it per encoder (L-HW-13).
	Preset string
	// Quality is the Quality slider's position (1..5) the CRF came from. It is only read for
	// L-HW-12's measured card offsets; 0 means the plan does not say.
	Quality int
	Scale   Scale
	// FPS is the output frame rate; zero keeps the source's.
	FPS      float64
	BitDepth BitDepth
	HDR      HDRMode
}

// RateControl is page 07's VBR/CBR choice: a bitrate in place of a CRF. The flow fills in the
// numbers from the ladder (or from the person's own typing); Build turns them into the encoder's
// rate-control arguments. TwoPass is not built yet, so Build refuses it rather than encode half
// of what the plan asked for.
type RateControl struct {
	Mode             string // "vbr" or "cbr"
	AvgMbps, MaxMbps float64
	TwoPass          bool
}

// Plan is what the steps fill and Build turns into one ffmpeg command. It is pure data: no
// services, no clock, no file system.
type Plan struct {
	Video VideoPlan
	// Audio says what happens to each named audio stream, including the codec and channel choices
	// when it converts; a stream it does not name is copied.
	Audio []AudioPlan
	// Subtitles says what happens to named input streams; unnamed streams are copied, except that
	// the file type decides how each one is written (Build's container rules).
	Subtitles []StreamPlan
	// SidecarDir is the folder extracted subtitles are written to, before they are placed beside
	// the new file (L-AUD-25). It is required when the plan extracts any.
	SidecarDir string
	// MetadataClear names per-stream video tags to remove from the output. The ffmpeg statistics
	// tags are always cleared on a re-encoded video stream, because they describe the old encode.
	MetadataClear []string
	Container     Container
	Output        string
	// RateControl replaces the CRF when it is set; Video.CRF is then nil and ignored.
	RateControl *RateControl
}

// Family is the encoder family a rung names. Stage 1 builds the processor only.
type Family string

const (
	FamilyCPU   Family = "cpu"
	FamilyQSV   Family = "qsv"
	FamilyNVENC Family = "nvenc"
	FamilyVAAPI Family = "vaapi"
	FamilyAMF   Family = "amf"
)

// Decode is where the decoder runs. Stage 1 builds software decode only.
type Decode string

const (
	DecodeSoftware Decode = "software"
	DecodeHardware Decode = "hardware"
)

// Rung is one attempt's encoder: what hw.Pick chose. Encoder is ffmpeg's name for it; Device is
// the render node path or index for the families that need one.
type Rung struct {
	Encoder string
	Family  Family
	Decode  Decode
	Device  string
}

// Result is the command plus what Build had to leave out. Notes are sentences for the log: a
// stream the container cannot hold, a Dolby Vision layer written as its HDR10 base.
type Result struct {
	Argv  []string
	Notes []string
}

// CPUEncoder is the processor encoder for a codec (L-HW-13).
func CPUEncoder(codec Codec) (string, bool) {
	switch codec {
	case CodecHEVC:
		return "libx265", true
	case CodecH264:
		return "libx264", true
	case CodecAV1:
		return "libsvtav1", true
	}
	return "", false
}

// presets maps the page's named choice to each encoder's own name. Every list has "slower"; the
// AV1 numbers are SVT-AV1's.
var presets = map[Codec]map[string]string{
	CodecHEVC: {"faster": "fast", "normal": "medium", "slower": "slow"},
	CodecH264: {"faster": "fast", "normal": "medium", "slower": "slow"},
	CodecAV1:  {"faster": "8", "normal": "6", "slower": "4"},
}

// tags is the container codec tag each encoder writes (L-HW-13).
var tags = map[Codec]string{
	CodecHEVC: "hvc1",
	CodecH264: "avc1",
	CodecAV1:  "av01",
}

// CardEncoder is a card family's encoder name for a codec: hevc_qsv, h264_vaapi, av1_nvenc and so
// on (L-HW-10). A family or codec it does not name is not a GPU rung. Whether the card really has
// the encoder is the probe's question (internal/hw); this is only the name to ask for.
func CardEncoder(family Family, codec Codec) (string, bool) {
	var suffix string
	switch family {
	case FamilyQSV:
		suffix = "qsv"
	case FamilyVAAPI:
		suffix = "vaapi"
	case FamilyNVENC:
		suffix = "nvenc"
	default:
		return "", false
	}
	switch codec {
	case CodecHEVC, CodecH264, CodecAV1:
		return string(codec) + "_" + suffix, true
	}
	return "", false
}

// cardQualityOffsets is L-HW-12's measured table: how far a card encoder's quality number moves
// from the processor's CRF at each Quality slider position. Only QSV's HEVC encoder was measured;
// every other family and format keeps the CRF as it is.
var cardQualityOffsets = map[string]map[string]int{
	"qsv": {"1": -2, "2": -2, "3": -1, "4": -1, "5": -1},
}

// CardQualityOffsets is L-HW-12's table, for the editor's ladder, which shows the numbers it will
// write. The returned table is shared: callers must not change it.
func CardQualityOffsets() map[string]map[string]int { return cardQualityOffsets }

// cardQualityOffset is what qualityArgs adds to the CRF for a card rung. The measurements are QSV
// HEVC's alone; another family or format uses the CRF as it is, and a plan that does not say which
// slider position its CRF came from (0) gets no offset.
func cardQualityOffset(family Family, codec Codec, quality int) int {
	if family != FamilyQSV || codec != CodecHEVC || quality < 1 || quality > 5 {
		return 0
	}
	return cardQualityOffsets["qsv"][strconv.Itoa(quality)]
}
