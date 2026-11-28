// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// tagOf is one struct tag of a step's settings field, read from the steps' own declarations, so a test never pins a
// word that the form could change.
func tagOf(v any, field, tag string) string {
	f, _ := reflect.TypeOf(v).FieldByName(field)
	return f.Tag.Get(tag)
}

// choiceLabelOf is the label a choices tag gives one value ("aac=AAC,opus=Opus" gives "Opus" for "opus").
func choiceLabelOf(choices, value string) string {
	for _, pair := range strings.Split(choices, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k == value {
			return v
		}
	}
	return ""
}

// The quality word is the slider's own label for the level, at both ends and in the middle.
func TestQualityWordFollowsTheSlider(t *testing.T) {
	labels := strings.Split(tagOf(steps.VideoSettings{}, "Quality", "sliderLabels"), ",")
	for _, level := range []int{1, 3, 5} {
		f := setStep(t, starter(t), "video.convert", "quality", level)
		c, _, _ := sentencesOf(t, f, 7)
		want := labels[level-1]
		if !strings.Contains(c, "at the "+want+" setting") {
			t.Errorf("quality %d: converts = %q, want the slider's %q", level, c, want)
		}
	}
}

// The audio codec is named by its choice's label.
func TestAudioWordFollowsTheChoices(t *testing.T) {
	choices := tagOf(steps.AudioSettings{}, "Codec", "choices")
	f := setStep(t, starter(t), "audio.tracks", "codec", "opus")
	c, _, _ := sentencesOf(t, f, 7)
	want := choiceLabelOf(choices, "opus")
	if want == "" || !strings.Contains(c, "Converts audio to "+want+", except") {
		t.Errorf("converts = %q, want the choice's label %q", c, want)
	}
}
