// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"fmt"
	"slices"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// VideoSettings is page 07's Video card, simple fields only: the advanced ones (size, rate
// control, preset, bit depth, HDR, fps) are stage 2.
type VideoSettings struct {
	Format     string `set:"format" label:"Format" choices:"hevc,h264,av1" default:"hevc"`
	Quality    int    `set:"quality" label:"Quality" min:"1" max:"5" default:"3"`
	UseGpu     bool   `set:"useGpu" label:"Use the graphics card" default:"true"`
	IfGpuFails string `set:"ifGpuFails" label:"If the graphics card fails" choices:"cpu,stop" default:"cpu"`
}

func init() { flow.RegisterStep(VideoStep{}) }

// VideoStep converts the picture to the chosen format at the chosen quality.
type VideoStep struct{}

var _ flow.Step = VideoStep{}

// Def is the step's own description.
func (VideoStep) Def() flow.Definition {
	return flow.Definition{
		Type:     "video.convert",
		Version:  1,
		Name:     "Video",
		Help:     "Choose the format and how small the picture should get. The file is not touched until the end.",
		Settings: VideoSettings{},
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
	if !set.UseGpu {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "ifGpuFails", Why: "The graphics card is not used."})
	} else if vc.Env.CardFamily == "" {
		c.Disabled = append(c.Disabled, flow.Disabled{Field: "useGpu", Why: "No worker has a graphics card."})
	}

	gpu := "processor only"
	if set.UseGpu && vc.Env.CardFamily != "" {
		gpu = "graphics card automatic"
	}
	c.Summary = fmt.Sprintf("%s · quality %d · %s", codecName(encode.Codec(set.Format)), set.Quality, gpu)
	return c
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
