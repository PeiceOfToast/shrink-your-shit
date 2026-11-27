// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// audioDefaults is the settings a flow gets when its Audio JSON names nothing: the card's own
// defaults, as DecodeSettings fills them.
func audioDefaults() AudioSettings {
	return AudioSettings{
		Format: "copy", Channels: "source", BitratePer: "channel",
		Commentary: "keep", Subtitles: "keep_all", Downmix: ptr("plain"),
	}
}

func ptr[T any](v T) *T { return &v }

func TestAudioAsIs(t *testing.T) {
	var p encode.Plan
	info := probeInfo("h264", 1920, 1080)
	d, err := AudioStep{}.Plan(audioDefaults(), flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "audio.as_is" || d.Label != "Audio copied as is" {
		t.Errorf("decision = %+v, want audio.as_is", d)
	}
	if d.Detail["audioTracks"] != 2 || d.Detail["subtitleTracks"] != 1 {
		t.Errorf("detail = %v, want 2 audio and 1 subtitle", d.Detail)
	}
	if len(p.Audio) != 0 || len(p.Subtitles) != 0 {
		t.Errorf("plan streams = %+v, want everything copied by default", p)
	}
}

func TestAudioValidate(t *testing.T) {
	check := AudioStep{}.Validate(audioDefaults(), flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("copy problems = %+v, want none", check.Problems)
	}
	if check.Summary != "Audio copied as is" {
		t.Errorf("summary = %q", check.Summary)
	}
	for _, format := range []string{"aac", "opus"} {
		set := audioDefaults()
		set.Format = format
		check = AudioStep{}.Validate(set, flow.ValidateContext{})
		if len(check.Problems) != 1 || check.Problems[0].Field != "format" || check.Problems[0].Code != "audio.not_yet" {
			t.Errorf("%s problems = %+v, want the not-yet problem", format, check.Problems)
		}
	}
	set := audioDefaults()
	set.Format = "flac"
	check = AudioStep{}.Validate(set, flow.ValidateContext{})
	if len(check.Problems) != 1 || check.Problems[0].Code != "setting.choice" {
		t.Errorf("flac problems = %+v, want the choice problem", check.Problems)
	}
}

func TestAudioDefaults(t *testing.T) {
	s, err := flow.DecodeSettings(AudioStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := s.(AudioSettings); got.Format != "copy" {
		t.Errorf("default = %q, want copy", got.Format)
	}
}

// TestAudioNotYet pins the audio and subtitle settings the check reports "not yet" until item 7:
// one problem per field that asks for something the engine does not do.
func TestAudioNotYet(t *testing.T) {
	set := audioDefaults()
	set.Channels = "stereo"
	set.Languages = []string{"eng"}
	set.Codec = ptr("opus")
	set.BitrateKbps = ptr(128)
	set.BitratePer = "track"
	set.SampleRate = ptr(48000)
	set.Downmix = ptr("dpl2")
	set.Loudnorm = true
	set.Commentary = "drop"
	set.DefaultTrack = ptr("eng")
	set.KeepOriginal = true
	set.Subtitles = "my_languages"
	set.SubtitleLanguages = []string{"eng"}
	check := AudioStep{}.Validate(set, flow.ValidateContext{})
	got := map[string]bool{}
	for _, p := range check.Problems {
		if p.Code != "audio.not_yet" {
			t.Errorf("problem = %+v, want audio.not_yet", p)
		}
		got[p.Field] = true
	}
	for _, field := range []string{"channels", "languages", "codec", "bitrateKbps", "bitratePer",
		"sampleRate", "downmix", "loudnorm", "commentary", "defaultTrack", "keepOriginal",
		"subtitles", "subtitleLanguages"} {
		if !got[field] {
			t.Errorf("problems = %v, want one on %s", got, field)
		}
	}
	// A codec in Advanced replaces the Format row, said by the check.
	disabled := false
	for _, d := range check.Disabled {
		if d.Field == "format" && strings.Contains(d.Why, "Opus") {
			disabled = true
		}
	}
	if !disabled {
		t.Errorf("disabled = %+v, want Format greyed by the codec", check.Disabled)
	}
	// The card's line names what was asked for, not the copy it is not.
	if !strings.HasPrefix(check.Summary, "Opus · stereo") {
		t.Errorf("summary = %q, want the codec and channels", check.Summary)
	}

	// The defaults are the engine's current behaviour: nothing to report.
	def, err := flow.DecodeSettings(AudioStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if problems := (AudioStep{}).Validate(def, flow.ValidateContext{}).Problems; len(problems) != 0 {
		t.Errorf("defaults problems = %+v, want none", problems)
	}
}
