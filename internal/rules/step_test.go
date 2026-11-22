// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// stepFunc is a test Step: it sets one word on the Plan's container and returns one decision.
type stepFunc struct {
	container, words string
}

func (s stepFunc) Apply(_ media.Info, p *Plan) []Decision {
	p.Container = s.container
	return []Decision{{NodeID: s.container, Port: "match", Words: s.words}}
}

// A step contributes through Apply, and the compile collects every step's decisions in the order the steps ran.
func TestCompileCollectsStepDecisionsInOrder(t *testing.T) {
	steps := []Step{stepFunc{"mkv", "first"}, stepFunc{"mp4", "second"}, stepFunc{"", "third"}}
	p := runSteps(Plan{Kind: KindSelectors}, media.Info{}, steps)
	want := []Decision{
		{NodeID: "mkv", Port: "match", Words: "first"},
		{NodeID: "mp4", Port: "match", Words: "second"},
		{NodeID: "", Port: "match", Words: "third"},
	}
	if !reflect.DeepEqual(p.Decisions, want) {
		t.Errorf("decisions %+v, want %+v (in run order)", p.Decisions, want)
	}
	if p.Container != "" {
		t.Errorf("container %q, want the last step's", p.Container)
	}
}

// A step that decides nothing leaves no decisions behind: a flow with only the built-in Selectors step keeps the
// Plan's Decisions empty, so its fingerprint does not change.
func TestSelectorsStepAddsNoDecisions(t *testing.T) {
	s := DefaultSelectors(3)
	p := runSteps(Plan{Kind: KindSelectors}, probe("h264", 1920, 1080, 24, 9000), []Step{SelectorsStep{S: s}})
	if p.Decisions != nil {
		t.Errorf("decisions %+v, want none from the Selectors step", p.Decisions)
	}
}

// stepSelectors is a standard flow that keeps only the person's languages in its audio.
func stepSelectors(t *testing.T) Selectors {
	t.Helper()
	s := DefaultSelectors(3)
	s.Audio, s.OnlyMyLanguages, _ = StandardAudio("my_languages", true)
	s.FileType = "keep"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	return s
}

// stepInputs are one h264 and one hevc file, each with English and Japanese audio.
func stepInputs() map[string]media.Info {
	audio := func() []media.Stream {
		return []media.Stream{
			{Type: "audio", Index: 1, Codec: "aac", Channels: 2, Language: "eng"},
			{Type: "audio", Index: 2, Codec: "aac", Channels: 2, Language: "jpn"},
		}
	}
	return map[string]media.Info{
		"h264": probe("h264", 1920, 1080, 24, 8000, audio()...),
		"hevc": probe("hevc", 1920, 1080, 24, 8000, audio()...),
	}
}

// The Selectors step passes the languages and the basic flag on: the plan equals a direct compile with the same
// arguments. A language choice that changes the plan makes a dropped argument visible.
func TestSelectorsStepForwardsLanguagesAndBasic(t *testing.T) {
	s := stepSelectors(t)
	langSets := [][]string{nil, {"eng"}, {"jpn", "eng"}}
	for name, info := range stepInputs() {
		for _, basic := range []bool{false, true} {
			for _, langs := range langSets {
				want := compileSelectors(Plan{Kind: KindSelectors}, s, info, langs, basic)
				got := runSteps(Plan{Kind: KindSelectors}, info, []Step{SelectorsStep{S: s, Langs: langs, Basic: basic}})
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s basic=%v langs=%v: step plan %+v, direct compile %+v", name, basic, langs, got, want)
				}
			}
		}
	}
	info := stepInputs()["h264"]
	one := compileSelectors(Plan{Kind: KindSelectors}, s, info, []string{"eng"}, false)
	none := compileSelectors(Plan{Kind: KindSelectors}, s, info, nil, false)
	if reflect.DeepEqual(one, none) {
		t.Errorf("the languages do not change the plan, so the grid above cannot catch a dropped language")
	}
}

// A Template carries its languages into the Selectors compile: Languages set keeps only English here.
func TestTemplateCompilesWithItsLanguages(t *testing.T) {
	tmpl := stepSelectors(t).Template()
	tmpl.Languages = "eng"
	p := tmpl.Compile(stepInputs()["h264"])
	if len(p.Tracks) != 1 || !p.Tracks[0].Drop || p.Tracks[0].Index != 2 {
		t.Errorf("languages dropped on the way to the plan: tracks %+v, want the Japanese track dropped", p.Tracks)
	}
}
