// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// G2 (#724 review2 P3): a step's own language list survives FromTemplate -> ToTemplate, and a list set by hand
// on a step's config reads into the template's selectors.
func stepLangSelectors(t *testing.T) rules.Selectors {
	t.Helper()
	sel := rules.DefaultSelectors(3)
	sel.Audio, sel.OnlyMyLanguages, _ = rules.StandardAudio("my_languages", true)
	sel.AudioLangs = []string{"eng", "ger"}
	sel.Subtitles = "my_languages"
	sel.SubLangs = []string{"fre"}
	return sel
}

func TestStepLanguagesRoundTrip(t *testing.T) {
	sel := stepLangSelectors(t)
	tmpl := sel.Template()
	d := FromTemplate(tmpl, nil)
	back, issues := ToTemplate(d, tmpl)
	if back.Selectors == nil {
		t.Fatalf("no selectors back, issues %+v", issues)
	}
	if !slices.Equal(back.Selectors.AudioLangs, []string{"eng", "ger"}) || !slices.Equal(back.Selectors.SubLangs, []string{"fre"}) {
		t.Errorf("round trip lost the step lists: audio %v, subtitles %v", back.Selectors.AudioLangs, back.Selectors.SubLangs)
	}
}

func TestHandSetStepLanguagesReadIntoSelectors(t *testing.T) {
	tmpl := stepLangSelectors(t).Template()
	d := FromTemplate(tmpl, nil)
	set := map[string]bool{}
	for i := range d.Nodes {
		switch d.Nodes[i].Type {
		case TypeAudio:
			d.Nodes[i].Config["languages"] = []any{"fre", "eng"}
			set[TypeAudio] = true
		case TypeSubtitles:
			d.Nodes[i].Config["languages"] = []any{"ger"}
			set[TypeSubtitles] = true
		}
	}
	if !set[TypeAudio] || !set[TypeSubtitles] {
		t.Fatalf("the flow has no audio and subtitles steps: %v", set)
	}
	back, _ := ToTemplate(d, tmpl)
	if back.Selectors == nil || !slices.Equal(back.Selectors.AudioLangs, []string{"fre", "eng"}) || !slices.Equal(back.Selectors.SubLangs, []string{"ger"}) {
		t.Errorf("hand-set step lists not read: %+v", back.Selectors)
	}
}
