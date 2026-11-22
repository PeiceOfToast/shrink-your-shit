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
