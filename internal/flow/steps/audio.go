// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// AudioSettings is page 07's Audio card: the format, the channels and the languages to keep,
// then Advanced's codec, bitrate, sample rate, downmix, loudness, commentary and default track,
// then the subtitles. Every field's tags are the page's form; the engine copies every track until
// item 7 builds the rest, which Validate reports.
type AudioSettings struct {
	Format    string   `set:"format" label:"Format" choices:"aac=AAC,opus=Opus,copy=Same as the source" default:"copy"`
	Channels  string   `set:"channels" label:"Channels" choices:"stereo=Stereo,source=Same as the source,mono=Mono,5.1=5.1" default:"source" help:"A most, never an upmix: a stereo file stays stereo wherever 5.1 is asked for."`
	Languages []string `set:"languages" label:"Keep these languages" widget:"languages" help:"Other tracks are removed; if none match, the main track stays. Original language keeps the film's own language, whatever it is."`

	Codec        *string `set:"codec" label:"Codec" group:"advanced" choices:"aac=AAC,opus=Opus,ac3=AC-3,eac3=E-AC-3,flac=FLAC,copy=Copy the original" unset:"As picked above" widget:"select"`
	BitrateKbps  *int    `set:"bitrateKbps" label:"Bitrate" group:"advanced" unit:"kb/s" min:"8" max:"1536"`
	BitratePer   string  `set:"bitratePer" label:"Bitrate is for" group:"advanced" choices:"channel=Each channel,track=The whole track" default:"channel" showIf:"bitrateKbps!=null"`
	SampleRate   *int    `set:"sampleRate" label:"Sample rate" group:"advanced" unit:"Hz" min:"8000" max:"192000"`
	Downmix      *string `set:"downmix" label:"Making stereo from surround" group:"advanced" choices:"speech=Clear speech,plain=Plain mix,dpl2=Dolby Pro Logic II" default:"plain" widget:"select"`
	Loudnorm     bool    `set:"loudnorm" label:"Even out loudness" group:"advanced" default:"false"`
	Commentary   string  `set:"commentary" label:"Commentary tracks" group:"advanced" choices:"keep=Keep,drop=Drop" default:"keep"`
	DefaultTrack *string `set:"defaultTrack" label:"Default track" group:"advanced" widget:"select"`
	KeepOriginal bool    `set:"keepOriginal" label:"Keep the original track as well" group:"advanced" default:"false"`

	Subtitles         string   `set:"subtitles" label:"Subtitles" group:"subtitles" choices:"keep_all=Keep all,my_languages=Keep my languages,text_only=Text only,srt=Make SRT files" default:"keep_all" help:"Text only drops picture subtitles, which some TVs can't show. Make SRT files saves them next to the video."`
	SubtitleLanguages []string `set:"subtitleLanguages" label:"Subtitle languages" group:"subtitles" widget:"languages" showIf:"subtitles=my_languages" help:"Empty keeps the same languages as the audio."`
}

func init() { flow.RegisterStep(AudioStep{}) }

// AudioStep carries the audio and subtitle tracks through untouched in stage 2's first items.
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
		Sections: []flow.Section{
			{ID: "advanced", Label: "Advanced"},
			{ID: "subtitles", Label: "Subtitles", Help: "The flow has three steps, so the subtitles live here."},
		},
		Events: []flow.EventDef{
			{Code: "audio.as_is", Level: events.LevelInfo, Text: "Audio copied as is",
				Fields: []string{"format", "audioTracks", "subtitleTracks"}, Keep: events.KeepLog},
		},
	}
}

// Plan always decides "nothing": a stream the plan does not name is copied, so there is nothing
// to add. It is the audio step's honest answer until conversion exists (item 7).
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

// Validate refuses what the engine cannot do yet, with a problem on each field, so page 07 marks
// them. Item 7 deletes these problems as it builds audio and subtitles.
func (AudioStep) Validate(s flow.Settings, vc flow.ValidateContext) flow.Check {
	set := s.(AudioSettings)
	var c flow.Check
	c.Problems = append(c.Problems, flow.TagProblems(AudioStep{}.Def(), set)...)
	c.Disabled = append(c.Disabled, audioDisabled(set)...)
	c.Problems = append(c.Problems, audioNotYet(set)...)
	c.Summary = audioSummary(set)
	return c
}

// audioDisabled is the greying the settings mean on their own: a codec in Advanced replaces the
// Format choice above it.
func audioDisabled(set AudioSettings) []flow.Disabled {
	if set.Codec == nil || *set.Codec == "" || *set.Codec == "copy" {
		return nil
	}
	return []flow.Disabled{{
		Field: "format",
		Why:   "Advanced is set: " + codecLabel(*set.Codec) + ". It replaces Format above.",
	}}
}

// audioNotYet is every setting the engine cannot act on yet (item 7).
func audioNotYet(set AudioSettings) []flow.Problem {
	var out []flow.Problem
	add := func(field, message string) {
		out = append(out, flow.Problem{Field: field, Code: "audio.not_yet", Message: message})
	}
	if set.Format == "aac" || set.Format == "opus" {
		add("format", "Converting audio is not built yet; Copy is the only choice for now.") // item 7
	}
	if set.Channels != "" && set.Channels != "source" {
		add("channels", "Changing the channels is not built yet; every track keeps the channels it has.") // item 7
	}
	if len(set.Languages) > 0 {
		add("languages", "Keeping only some languages is not built yet; every track is kept.") // item 7
	}
	if set.Codec != nil && *set.Codec != "" && *set.Codec != "copy" {
		add("codec", "Choosing the codec in Advanced is not built yet; Format decides for now.") // item 7
	}
	if set.BitrateKbps != nil {
		add("bitrateKbps", "Setting the audio bitrate is not built yet.") // item 7
	}
	if set.BitratePer == "track" && set.BitrateKbps != nil {
		add("bitratePer", "Setting one bitrate for the whole track is not built yet.") // item 7
	}
	if set.SampleRate != nil {
		add("sampleRate", "Changing the sample rate is not built yet.") // item 7
	}
	// Plain (the default) is what a copied track keeps anyway; only the two mixes change it.
	if set.Downmix != nil && *set.Downmix != "" && *set.Downmix != "plain" {
		add("downmix", "Making stereo from surround is not built yet.") // item 7
	}
	if set.Loudnorm {
		add("loudnorm", "Evening out loudness is not built yet.") // item 7
	}
	if set.Commentary == "drop" {
		add("commentary", "Dropping commentary tracks is not built yet.") // item 7
	}
	if set.DefaultTrack != nil && *set.DefaultTrack != "" {
		add("defaultTrack", "Choosing the default track is not built yet.") // item 7
	}
	if set.KeepOriginal {
		add("keepOriginal", "Keeping the original track as well is not built yet.") // item 7
	}
	if set.Subtitles != "" && set.Subtitles != "keep_all" {
		add("subtitles", "Choosing which subtitles to keep is not built yet; every subtitle is kept.") // item 7
	}
	if len(set.SubtitleLanguages) > 0 {
		add("subtitleLanguages", "Keeping only some subtitle languages is not built yet; every subtitle is kept.") // item 7
	}
	return out
}

// audioSummary is the closed card's line: the output format and channels, or the copy it is.
func audioSummary(set AudioSettings) string {
	name := ""
	switch {
	case set.Codec != nil && *set.Codec != "" && *set.Codec != "copy":
		name = codecLabel(*set.Codec)
	case set.Format != "" && set.Format != "copy":
		name = codecLabel(set.Format)
	}
	if name == "" {
		return "Audio copied as is"
	}
	return name + " · " + channelsLabel(set.Channels)
}

// codecLabel is how the pages write an audio codec.
func codecLabel(codec string) string {
	switch codec {
	case "aac":
		return "AAC"
	case "opus":
		return "Opus"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	case "flac":
		return "FLAC"
	case "copy":
		return "copy"
	}
	return codec
}

// channelsLabel is how the pages write a Channels choice.
func channelsLabel(channels string) string {
	switch channels {
	case "stereo":
		return "stereo"
	case "mono":
		return "mono"
	case "5.1":
		return "5.1"
	}
	return "same channels"
}
