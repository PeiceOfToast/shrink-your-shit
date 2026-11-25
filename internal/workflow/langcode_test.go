// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// QA 724: a made-up code ("zzz") is refused in the guide's answers, the same as in Settings and in a step's
// own list. Real codes in any form (eng, fre, en) are accepted.
func TestGuideRefusesAnUnknownLanguageCode(t *testing.T) {
	langIssue := func(a Answers) (Issue, bool) {
		for _, iss := range Check(a) {
			if iss.Field == "languages" || iss.Field == "subtitleLanguages" {
				return iss, true
			}
		}
		return Issue{}, false
	}

	if iss, bad := langIssue(Answers{Languages: []string{"zzz"}, LanguagesEverywhere: true}); !bad || iss.Code != "invalid_answer" || !strings.Contains(iss.Message, "zzz") {
		t.Errorf("audio zzz: want invalid_answer naming zzz on languages, got %+v (refused %v)", iss, bad)
	}
	if iss, bad := langIssue(Answers{Languages: []string{"eng"}, SubtitleLanguages: []string{"zzz"}, LanguagesEverywhere: true}); !bad || iss.Field != "subtitleLanguages" {
		t.Errorf("subtitle zzz: want a refusal on subtitleLanguages, got %+v (refused %v)", iss, bad)
	}
	if iss, bad := langIssue(Answers{Languages: []string{"eng", "fre", "jpn", "en", "ENG"}, LanguagesEverywhere: true}); bad {
		t.Errorf("real codes refused: %+v", iss)
	}
}

// The step's own list (G2) refuses a made-up code too, on both the Audio and the Subtitles step.
func TestStepConfigRefusesAnUnknownLanguageCode(t *testing.T) {
	sel := rules.DefaultSelectors(3)
	sel.Audio, sel.OnlyMyLanguages, _ = rules.StandardAudio("my_languages", true)
	sel.AudioLangs = []string{"eng"}
	for _, typ := range []string{TypeAudio, TypeSubtitles} {
		const field = "languages"
		d := FromTemplate(sel.Template(), nil)
		found := false
		for i := range d.Nodes {
			if d.Nodes[i].Type == typ {
				d.Nodes[i].Config[field] = []any{"eng", "zzz"}
				found = true
			}
		}
		if !found {
			t.Skipf("no %s step in the flow", typ)
		}
		r := Validate(d)
		if !r.Errors() || !strings.Contains(r.Issues[0].Message, `"zzz"`) {
			t.Errorf("%s step zzz: %+v", typ, r.Issues)
		}
	}
}
