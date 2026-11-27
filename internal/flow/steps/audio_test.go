// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

func TestAudioAsIs(t *testing.T) {
	var p encode.Plan
	info := probeInfo("h264", 1920, 1080)
	d, err := AudioStep{}.Plan(AudioSettings{Format: "copy"}, flow.PlanInput{Info: info}, &p)
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
	check := AudioStep{}.Validate(AudioSettings{Format: "copy"}, flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("copy problems = %+v, want none", check.Problems)
	}
	if check.Summary != "Audio copied as is" {
		t.Errorf("summary = %q", check.Summary)
	}
	for _, format := range []string{"aac", "opus"} {
		check = AudioStep{}.Validate(AudioSettings{Format: format}, flow.ValidateContext{})
		if len(check.Problems) != 1 || check.Problems[0].Field != "format" || check.Problems[0].Code != "audio.not_yet" {
			t.Errorf("%s problems = %+v, want the not-yet problem", format, check.Problems)
		}
	}
	check = AudioStep{}.Validate(AudioSettings{Format: "flac"}, flow.ValidateContext{})
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
