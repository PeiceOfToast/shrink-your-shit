// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// VideoSettings is page 07's Video card: the simple fields, then Advanced's picture size,
// bitrate, speed, frame rate, bit depth and HDR. Every field's tags are the page's form; the
// engine acts on the simple fields and the bitrate, until item 8 builds the rest, which Validate
// reports.
type VideoSettings struct {
	Format  string `set:"format" label:"Format" choices:"hevc=HEVC,h264=H.264,av1=AV1" default:"hevc" help:"HEVC makes the smallest files. H.264 plays everywhere. AV1 is smallest but slow to encode."`
	Quality int    `set:"quality" label:"Quality" min:"1" max:"5" default:"3" widget:"slider" sliderLabels:"Smallest file,Smaller,Balanced,Better quality,Best quality" help:"Lower means a better picture and a bigger file."`
	UseGpu  bool   `set:"useGpu" label:"Graphics card" default:"true" help:"Use a graphics card when a worker has one."`
	// IfGpuFails decides what happens when the chosen card cannot encode the file.
	IfGpuFails string `set:"ifGpuFails" label:"If the graphics card can't do it" choices:"cpu=Use the processor,stop=Stop and tell me" default:"cpu"`

	Size            *string  `set:"size" label:"Picture size" group:"advanced" choices:"2160=2160p,1080=1080p,720=720p,480=480p,custom=Custom (width × height)" unset:"Same as the source" help:"Never made bigger than the source; the shape is kept."`
	Width           *int     `set:"width" label:"Width" group:"advanced" unit:"px" min:"16" max:"7680" showIf:"size=custom"`
	Height          *int     `set:"height" label:"Height" group:"advanced" unit:"px" min:"16" max:"7680" showIf:"size=custom"`
	RateControl     *string  `set:"rateControl" label:"Bitrate" group:"advanced" choices:"vbr=Variable bitrate,cbr=Constant bitrate" unset:"Quality (the slider)" help:"A bitrate replaces the Quality slider."`
	BitrateLevel    string   `set:"bitrateLevel" label:"Recommended" group:"advanced" choices:"low=Low,medium=Medium,high=High,custom=Custom" default:"medium" showIf:"rateControl!=null" help:"Fills in the numbers for the picked size and format."`
	AvgMbps         *float64 `set:"avgMbps" label:"Average" group:"advanced" unit:"Mb/s" min:"0.1" max:"1000" showIf:"rateControl!=null"`
	MaxMbps         *float64 `set:"maxMbps" label:"At most" group:"advanced" unit:"Mb/s" min:"0.1" max:"1000" showIf:"rateControl=vbr"`
	TwoPass         bool     `set:"twoPass" label:"Two passes" group:"advanced" showIf:"rateControl=vbr" help:"Slower, but the size stays closer to the average."`
	Preset          *string  `set:"preset" label:"Speed" group:"advanced" choices:"faster=Faster,normal=Balanced,slower=Slower and smaller" default:"normal" widget:"select"`
	FPS             *float64 `set:"fps" label:"Frame rate" group:"advanced" min:"1" max:"240" help:"Left empty, each file keeps its own frame rate."`
	BitDepth        *int     `set:"bitDepth" label:"Colour depth" group:"advanced" choices:"8=8-bit,10=10-bit" unset:"Same as the source"`
	HDR             *string  `set:"hdr" label:"HDR" group:"advanced" choices:"keep=Keep,tonemap=Convert to SDR,skip=Leave HDR files alone" default:"keep"`
	KeepDolbyVision bool     `set:"keepDolbyVision" label:"Keep Dolby Vision" group:"advanced" default:"false" help:"Only with HEVC."`
}

func init() { flow.RegisterStep(VideoStep{}) }

// VideoStep converts the picture to the chosen format at the chosen quality.
type VideoStep struct{}

var _ flow.Step = VideoStep{}

// VideoFormat is one of the settings facts the flow's container rules read (flow/containers.go):
// the format this step will write.
func (set VideoSettings) VideoFormat() string { return set.Format }

// Def is the step's own description.
func (VideoStep) Def() flow.Definition {
	return flow.Definition{
		Type:     "video.convert",
		Version:  1,
		Name:     "Video",
		Help:     "Choose the format and how small the picture should get. The file is not touched until the end.",
		Settings: VideoSettings{},
		Sections: []flow.Section{{
			ID: "advanced", Label: "Advanced",
			Help: "Picture size, bitrate, speed, frame rate, bit depth, HDR",
		}},
		Events: []flow.EventDef{
			{Code: "video.convert", Level: events.LevelInfo, Text: "Converting to {format}",
				Fields: []string{"format", "from", "crf"}, Keep: events.KeepLog},
			{Code: "video.already_target", Level: events.LevelInfo, Text: "Already {format}",
				Fields: []string{"format"}, Keep: events.KeepLog},
			{Code: "video.no_video", Level: events.LevelError, Text: "The file has no picture to convert",
				Keep: events.KeepLog},
			{Code: "video.no_bitrate", Level: events.LevelError, Text: "The bitrate is missing",
				Keep: events.KeepLog},
			{Code: "video.hdr_h264", Level: events.LevelError, Text: "H.264 isn't used for HDR",
				Keep: events.KeepLog},
			{Code: "video.hdr_skipped", Level: events.LevelInfo, Text: "Left alone: {hdr} is not converted",
				Fields: []string{"hdr"}, Keep: events.KeepLog},
		},
	}
}

// Plan decides whether the picture changes and fills the video part of the plan. A quality plan
// takes the CRF page 07's slider table gives for this format, position and picture size; a bitrate
// plan takes the ladder's numbers instead and has no CRF.
func (VideoStep) Plan(s flow.Settings, in flow.PlanInput, p *encode.Plan) (flow.Decision, error) {
	set := s.(VideoSettings)
	main, ok := mainVideo(in.Info)
	if !ok {
		return flow.Decision{}, &flow.PlanError{Code: "video.no_video", Message: "The file has no picture to convert."}
	}
	want := encode.Codec(set.Format)
	if main.Codec == string(want) {
		return flow.Decision{
			Work: false, Label: "Already " + codecName(want), Reason: "video.already_target",
			Detail: flow.F{"format": string(want), "from": main.Codec},
		}, nil
	}

	// HDR skip leaves an HDR file alone, with the reason in the job (L-HW-16); nothing is planned.
	hdr := ""
	if set.HDR != nil {
		hdr = *set.HDR
	}
	if hdr == "skip" && main.HDRKind() != "SDR" {
		return flow.Decision{
			Work: false, Label: "Left alone: " + main.HDRKind(), Reason: "video.hdr_skipped",
			Detail: flow.F{"hdr": main.HDRKind()},
		}, nil
	}

	p.Video.Codec = want
	p.Video.Preset = presetChoice(set)
	if set.Size != nil {
		p.Video.Scale = sizeBox(set)
	}
	p.Video.FPS = fpsAsked(set, main)
	p.Video.BitDepth = bitDepthAsked(set)
	p.Video.HDR = encode.HDRKeep
	if hdr == "tonemap" && main.HDRKind() != "SDR" {
		p.Video.HDR = encode.HDRTonemap
	}
	if want == encode.CodecH264 && p.Video.HDR == encode.HDRKeep && main.HDRKind() != "SDR" {
		// H.264 is not used for HDR, by choice (#855): say so before the run, not as a failed job.
		return flow.Decision{}, &flow.PlanError{Code: "video.hdr_h264",
			Message: "H.264 isn't used for HDR, because few devices play it. Pick HEVC, or choose Convert to SDR."}
	}
	detail := flow.F{
		"format": string(want), "from": main.Codec,
		"useGpu": set.UseGpu, "ifGpuFails": set.IfGpuFails,
	}
	if set.RateControl != nil {
		// The ladder row is the output picture's: a 4K source scaled to 1080 is a 1080 file.
		// p.Video.Scale is what the flow will write (the picture-size setting fills it).
		row := ladderRow(main, p.Video.Scale)
		rate, err := rateControl(set, in.Info, main, row)
		if err != nil {
			return flow.Decision{}, err
		}
		p.RateControl = &rate
		detail["row"] = row
		detail["rateControl"] = rate.Mode
		detail["avgMbps"] = rate.AvgMbps
		if rate.Mode == "vbr" {
			detail["maxMbps"] = rate.MaxMbps
		}
	} else {
		row := ladderRow(main, p.Video.Scale)
		crf := videoCRF(set.Format, set.Quality, row)
		p.Video.CRF = &crf
		detail["crf"] = crf
		detail["quality"] = set.Quality
		detail["row"] = row
	}

	label := "Converting to " + codecName(want)
	if set.UseGpu {
		label += " on the graphics card"
	}
	return flow.Decision{Work: true, Label: label, Reason: "video.convert", Detail: detail}, nil
}

// rateControl turns the Advanced card's bitrate choice into the encoder's numbers. Low, Medium and
// High take the ladder's number for the output row and format, raised for HDR and 48 fps+ files,
// and never above the source's own rate; a VBR level's at-most is the ladder's factor times the
// average. Custom uses the numbers the person typed.
func rateControl(set VideoSettings, info media.Info, main media.VideoTrack, row string) (encode.RateControl, error) {
	ladder := flow.Ladder()
	mode := *set.RateControl
	level := set.BitrateLevel
	if level == "" {
		level = "medium"
	}
	rc := encode.RateControl{Mode: mode}
	if level == "custom" {
		if set.AvgMbps == nil {
			return rc, &flow.PlanError{Code: "video.no_bitrate", Message: "Type an average bitrate for Custom."}
		}
		rc.AvgMbps = *set.AvgMbps
		if mode == "vbr" && set.MaxMbps != nil {
			rc.MaxMbps = *set.MaxMbps
		}
	} else {
		mbps, ok := ladder.LevelFor(set.Format, row, level)
		if !ok {
			return rc, &flow.PlanError{Code: "video.no_bitrate",
				Message: "The recommended bitrates have no number for this format and picture size."}
		}
		if main.FrameRate.Float() >= 48 {
			mbps *= ladder.Factors.FPS60
		}
		if main.HDRKind() != "SDR" {
			mbps *= ladder.Factors.HDR
		}
		// The source's own rate is the ceiling: asking for more would spend space for nothing.
		source := sourceMbps(info, main)
		if source > 0 && mbps > source {
			mbps = source
		}
		rc.AvgMbps = mbps
		if mode == "vbr" {
			rc.MaxMbps = mbps * ladder.VBRMaxFactor
			if source > 0 && rc.MaxMbps > source {
				rc.MaxMbps = source
			}
		}
	}
	if mode == "vbr" && rc.MaxMbps == 0 {
		// Custom did not type an at-most: the ladder's factor is the default.
		rc.MaxMbps = rc.AvgMbps * ladder.VBRMaxFactor
	}
	return rc, nil
}

// presetChoice is the speed the person picked, "normal" when unset.
func presetChoice(set VideoSettings) string {
	if set.Preset == nil || *set.Preset == "" {
		return "normal"
	}
	return *set.Preset
}

// sizeBox is the picture-size box the plan scales into (L-HW-16's size rules live in encode.Build:
// the box is never grown). A preset height leaves the width free, so the shape is kept.
func sizeBox(set VideoSettings) encode.Scale {
	if set.Size == nil || *set.Size == "" {
		return encode.Scale{}
	}
	if *set.Size == "custom" {
		var box encode.Scale
		if set.Width != nil {
			box.Width = *set.Width
		}
		if set.Height != nil {
			box.Height = *set.Height
		}
		return box
	}
	h, err := strconv.Atoi(*set.Size)
	if err != nil {
		return encode.Scale{}
	}
	return encode.Scale{Height: h}
}

// fpsAsked is the frame rate the person asked for, or zero to keep the file's own. A rate at or
// above the source's is never asked for: the frame rate is only ever lowered.
func fpsAsked(set VideoSettings, main media.VideoTrack) float64 {
	if set.FPS == nil {
		return 0
	}
	if src := main.FrameRate.Float(); src > 0 && *set.FPS >= src {
		return 0
	}
	return *set.FPS
}

// bitDepthAsked is the colour depth the person asked for; unset keeps the source's.
func bitDepthAsked(set VideoSettings) encode.BitDepth {
	switch {
	case set.BitDepth == nil:
		return encode.DepthSource
	case *set.BitDepth == 10:
		return encode.Depth10
	default:
		return encode.Depth8
	}
}

// sourceMbps is the source video's own rate, from its stream or, when the container does not say
// (Matroska often does not), the file's overall rate. Zero when neither is known.
func sourceMbps(info media.Info, main media.VideoTrack) float64 {
	bps := main.BitRate
	if bps <= 0 {
		bps = info.Format.BitRate
	}
	if bps <= 0 {
		return 0
	}
	return float64(bps) / 1e6
}

// Validate checks what the tags cannot: an encoder no worker has, and the two card fields when
// there is no card. The summary is the closed card's line.
func (VideoStep) Validate(s flow.Settings, vc flow.ValidateContext) flow.Check {
	set := s.(VideoSettings)
	var c flow.Check
	c.Problems = append(c.Problems, flow.TagProblems(VideoStep{}.Def(), set)...)

	if encoder, known := encode.CPUEncoder(encode.Codec(set.Format)); known &&
		len(vc.Env.Encoders) > 0 && !slices.Contains(vc.Env.Encoders, encoder) {
		c.Disabled = append(c.Disabled, flow.Disabled{
			Field: "format", Value: set.Format,
			Why: fmt.Sprintf("No worker can encode %s: %s is not installed.", codecName(encode.Codec(set.Format)), encoder),
		})
	}
	c.Disabled = append(c.Disabled, tagDisabled(set)...)
	if set.BitDepth != nil && *set.BitDepth == 10 && set.Format == "h264" {
		// Greyed and a problem: a plan asking for ten-bit H.264 is refused rather than written as eight (#855).
		c.Problems = append(c.Problems, flow.Problem{Field: "bitDepth", Code: "video.bitdepth_h264",
			Message: "10-bit H.264 plays on few devices, so it isn't offered. Pick HEVC or AV1 for 10-bit."})
	}
	if !set.UseGpu {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "ifGpuFails", Why: "The graphics card is not used."})
	} else if vc.Env.CardFamily == "" {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "useGpu", Why: "No worker has a graphics card."})
	}
	if set.RateControl != nil {
		// The page reads vbrFormats and names no format itself; this is the same answer for a flow
		// that already has VBR and publishes before the page is opened.
		if *set.RateControl == "vbr" && !flow.Ladder().VBR(set.Format) {
			c.Disabled = append(c.Disabled, flow.Disabled{Field: "rateControl", Value: "vbr",
				Why: fmt.Sprintf("Variable bitrate doesn't work with %s yet. Pick Constant bitrate or Quality, or another format.",
					codecName(encode.Codec(set.Format)))})
		}
		if set.BitrateLevel == "custom" && set.AvgMbps == nil {
			c.Problems = append(c.Problems, flow.Problem{Field: "avgMbps", Code: "video.no_bitrate",
				Message: "Type an average bitrate for Custom, or pick Low, Medium or High."})
		}
	}
	c.Problems = append(c.Problems, videoNotYet(set)...)
	c.Summary = videoSummary(set)
	return c
}

// videoSummary is the closed card's line in page 07's words: the format, the quality and the
// graphics card, or the bitrate when Advanced sets one (then it names the bitrate, not the
// quality).
func videoSummary(set VideoSettings) string {
	name := codecName(encode.Codec(set.Format))
	if set.RateControl == nil {
		card := "off"
		if set.UseGpu {
			card = "automatic"
		}
		return fmt.Sprintf("%s · quality %d · graphics card %s", name, set.Quality, card)
	}
	rate := "variable"
	if *set.RateControl == "cbr" {
		rate = "constant"
	}
	// A level's number depends on each file's size, so the line names the rate only; Custom's typed
	// average is named now.
	if set.BitrateLevel == "custom" && set.AvgMbps != nil {
		return fmt.Sprintf("%s · %s bitrate about %s Mb/s", name, rate, strconv.FormatFloat(*set.AvgMbps, 'f', -1, 64))
	}
	return name + " · " + rate + " bitrate"
}

// tagDisabled is the greying the settings mean on their own, whatever the server: a bitrate
// replaces the Quality slider, and Dolby Vision needs HEVC output.
func tagDisabled(set VideoSettings) []flow.Disabled {
	var out []flow.Disabled
	if set.RateControl != nil {
		out = append(out, flow.Disabled{Field: "quality", Why: "Not used: Advanced sets a bitrate instead."})
	}
	if set.KeepDolbyVision && set.Format != "hevc" {
		out = append(out, flow.Disabled{Field: "keepDolbyVision", Why: "Only with HEVC."})
	} else if set.KeepDolbyVision {
		// Not faked (S2-8): the processor path cannot carry the RPU with the pinned ffmpeg.
		out = append(out, flow.Disabled{Field: "keepDolbyVision",
			Why: "Keeping the Dolby Vision layer is not possible on the processor with this ffmpeg. Profiles 7 and 8 convert as their HDR10 base."})
	}
	if set.BitDepth != nil && *set.BitDepth == 10 && set.Format == "h264" {
		out = append(out, flow.Disabled{Field: "bitDepth", Value: "10",
			Why: "10-bit H.264 plays on few devices, so it isn't offered. Pick HEVC or AV1 for 10-bit."})
	}
	return out
}

// videoNotYet is every Advanced setting the engine cannot act on yet. Two passes belong to item 6
// and are not built, so they still report.
func videoNotYet(set VideoSettings) []flow.Problem {
	var out []flow.Problem
	if set.TwoPass {
		out = append(out, flow.Problem{Field: "twoPass", Code: "video.not_yet",
			Message: "Two passes are not built yet; the encode makes one pass."})
	}
	return out
}

// The Quality slider, page 07's numbers at 1080p on the processor: each position's x265 CRF and
// the two moves that turn it into this file's number. A format's offset is added to the slider's
// number; the picture-size row shifts it by one at 2160p and by minus one at 720p and below.
var (
	videoSlider = [...]struct {
		Name string
		CRF  int
	}{
		{"Smallest file", 27},
		{"Smaller", 25},
		{"Balanced", 23},
		{"Better quality", 21},
		{"Best quality", 19},
	}
	videoCRFOffset = map[string]int{"hevc": 0, "h264": -5, "av1": 9}
	videoRowShift  = map[string]int{"2160": 1, "1440": 0, "1080": 0, "720": -1, "sd": -1}
)

// videoRow is one picture-size row of page 07's ladder, by its upright height.
var videoRows = []struct {
	Name   string
	Height int
}{
	{"2160", 2160},
	{"1440", 1440},
	{"1080", 1080},
	{"720", 720},
	{"sd", 480},
}

// videoCRF is the CRF this format, slider position and size row use on the processor. A position
// outside 1..5 cannot arrive through DecodeSettings; it is clamped so a hand-built settings value
// still plans something sane.
func videoCRF(format string, quality int, row string) int {
	pos := quality
	if pos < 1 {
		pos = 1
	}
	if pos > len(videoSlider) {
		pos = len(videoSlider)
	}
	return videoSlider[pos-1].CRF + videoCRFOffset[format] + videoRowShift[row]
}

// resolutionRow is the ladder row nearest the source's upright picture height.
func resolutionRow(v media.VideoTrack) string {
	h := v.Height
	if v.Rotation%180 != 0 {
		h = v.Width // a quarter turn makes the coded width the upright height
	}
	return resolutionRowHeight(h)
}

// ladderRow is the ladder row nearest the output picture's height: the source's when nothing is
// scaled, or the height the plan's scale box fits.
func ladderRow(main media.VideoTrack, scale encode.Scale) string {
	return resolutionRowHeight(encode.OutputHeight(main, scale))
}

// resolutionRowHeight is the ladder row nearest a height, so a 2160p picture gets the 2160 row and
// a 480p or smaller one the sd row. A tie goes to the higher row.
func resolutionRowHeight(h int) string {
	best := videoRows[0]
	for _, row := range videoRows[1:] {
		if abs(row.Height-h) < abs(best.Height-h) {
			best = row
		}
	}
	return best.Name
}

// mainVideo is the first video stream that is a picture rather than cover art.
func mainVideo(info media.Info) (media.VideoTrack, bool) {
	for _, v := range info.Video {
		if !v.AttachedPicture {
			return v, true
		}
	}
	return media.VideoTrack{}, false
}

// codecName is how the pages write a codec.
func codecName(c encode.Codec) string {
	switch c {
	case encode.CodecHEVC:
		return "HEVC"
	case encode.CodecH264:
		return "H.264"
	case encode.CodecAV1:
		return "AV1"
	}
	return string(c)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
