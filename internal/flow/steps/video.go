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
// engine acts on the simple ones until items 6 and 8 build the rest, which Validate reports.
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
		},
	}
}

// Plan decides whether the picture changes and fills the video part of the plan. The CRF is the
// one page 07's slider table gives for this format, position and picture size, on the processor.
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

	row := resolutionRow(main)
	crf := videoCRF(set.Format, set.Quality, row)
	p.Video.Codec = want
	p.Video.CRF = &crf
	p.Video.Preset = "normal"
	p.Video.BitDepth = encode.DepthSource
	p.Video.HDR = encode.HDRKeep

	label := "Converting to " + codecName(want)
	if set.UseGpu {
		label += " on the graphics card"
	}
	return flow.Decision{
		Work: true, Label: label, Reason: "video.convert",
		Detail: flow.F{
			"format": string(want), "from": main.Codec, "crf": crf,
			"quality": set.Quality, "row": row,
			"useGpu": set.UseGpu, "ifGpuFails": set.IfGpuFails,
		},
	}, nil
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
	if !set.UseGpu {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "ifGpuFails", Why: "The graphics card is not used."})
	} else if vc.Env.CardFamily == "" {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "useGpu", Why: "No worker has a graphics card."})
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
	// A level's number comes from the ladder (item 6); a typed average is named now.
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
	}
	return out
}

// videoNotYet is every Advanced setting the engine cannot act on yet. Items 6 and 8 delete these
// lines as they build the setting.
func videoNotYet(set VideoSettings) []flow.Problem {
	var out []flow.Problem
	add := func(field, message string) {
		out = append(out, flow.Problem{Field: field, Code: "video.not_yet", Message: message})
	}
	if set.Size != nil {
		add("size", "Choosing the picture size is not built yet; each file keeps its own size.") // item 8
	} else if set.Width != nil {
		add("width", "Choosing the picture width is not built yet; each file keeps its own size.") // item 8
	} else if set.Height != nil {
		add("height", "Choosing the picture height is not built yet; each file keeps its own size.") // item 8
	}
	if set.RateControl != nil {
		add("rateControl", "Setting a bitrate is not built yet; the Quality slider decides for now.") // item 6
	} else {
		for _, field := range []string{"avgMbps", "maxMbps", "twoPass"} {
			switch field {
			case "avgMbps":
				if set.AvgMbps != nil {
					add(field, "Setting a bitrate is not built yet; the Quality slider decides for now.") // item 6
				}
			case "maxMbps":
				if set.MaxMbps != nil {
					add(field, "Setting a bitrate is not built yet; the Quality slider decides for now.") // item 6
				}
			case "twoPass":
				if set.TwoPass {
					add(field, "Setting a bitrate is not built yet; the Quality slider decides for now.") // item 6
				}
			}
		}
	}
	if set.BitrateLevel != "" && set.BitrateLevel != "medium" {
		add("bitrateLevel", "Picking a bitrate level is not built yet; the Quality slider decides for now.") // item 6
	}
	if set.Preset != nil && *set.Preset != "" && *set.Preset != "normal" {
		add("preset", "Choosing the encoder speed is not built yet; the processor picks its own speed.") // item 8
	}
	if set.FPS != nil {
		add("fps", "Changing the frame rate is not built yet; each file keeps its frame rate.") // item 8
	}
	if set.BitDepth != nil {
		add("bitDepth", "Changing the colour depth is not built yet; each file keeps its own.") // item 8
	}
	if set.HDR != nil && *set.HDR != "" && *set.HDR != "keep" {
		add("hdr", "Converting HDR is not built yet; HDR files keep theirs.") // item 8
	}
	if set.KeepDolbyVision {
		add("keepDolbyVision", "Keeping the Dolby Vision layer is not built yet.") // item 8
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

// resolutionRow is the ladder row nearest the upright picture height, so a 2160p file gets the
// 2160 row and a 480p or smaller file the sd row. A tie goes to the higher row.
func resolutionRow(v media.VideoTrack) string {
	h := v.Height
	if v.Rotation%180 != 0 {
		h = v.Width // a quarter turn makes the coded width the upright height
	}
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
