// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// AudioSettings is page 07's Audio card. Stage 1 has the format choice only, and only "copy"
// works: converting audio is stage 2.
type AudioSettings struct {
	Format string `set:"format" label:"Audio format" choices:"aac,opus,copy" default:"copy"`
}

func init() { flow.RegisterStep(AudioStep{}) }

// AudioStep carries the audio and subtitle tracks through untouched in stage 1.
type AudioStep struct{}

var _ flow.Step = AudioStep{}

// Def is the step's own description.
func (AudioStep) Def() flow.Definition {
	return flow.Definition{
		Type:     "audio.tracks",
		Version:  1,
		Name:     "Audio",
		Help:     "Keep the sound as it is, or choose another format once that is built.",
		Settings: AudioSettings{},
		Events: []flow.EventDef{
			{Code: "audio.as_is", Level: events.LevelInfo, Text: "Audio copied as is",
				Fields: []string{"format", "audioTracks", "subtitleTracks"}, Keep: events.KeepLog},
		},
	}
}

// Plan always decides "nothing": a stream the plan does not name is copied, so there is nothing
// to add. It is the audio step's honest answer until conversion exists.
func (AudioStep) Plan(s flow.Settings, in flow.PlanInput, p *encode.Plan) (flow.Decision, error) {
	set := s.(AudioSettings)
	return flow.Decision{
		Work: false, Label: "Audio copied as is", Reason: "audio.as_is",
		Detail: flow.F{
			"format":         set.Format,
			"audioTracks":    len(in.Info.Audio),
			"subtitleTracks": len(in.Info.Subtitles),
		},
	}, nil
}

// Validate refuses a format stage 1 cannot produce, with a problem on the field.
func (AudioStep) Validate(s flow.Settings, vc flow.ValidateContext) flow.Check {
	set := s.(AudioSettings)
	var c flow.Check
	c.Problems = append(c.Problems, flow.TagProblems(AudioStep{}.Def(), set)...)
	if set.Format == "aac" || set.Format == "opus" {
		c.Problems = append(c.Problems, flow.Problem{
			Field: "format", Code: "audio.not_yet",
			Message: "Converting audio is not built yet; Copy is the only choice for now.",
		})
	}
	c.Summary = "Audio copied as is"
	return c
}
