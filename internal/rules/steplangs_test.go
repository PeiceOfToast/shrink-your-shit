// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// stepPlan compiles a Selectors with the person's Settings list langs, the way a run does.
func stepPlan(t *testing.T, s Selectors, langs string, info media.Info) Plan {
	t.Helper()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	tmpl.Languages = langs
	return tmpl.Compile(info)
}

func dropped(p Plan) (out []int) {
	for _, o := range p.Tracks {
		if o.Drop {
			out = append(out, o.Index)
		}
	}
	return out
}

// Two workflows with different lists, on one file, keep different tracks (G2). Without the per-step
// list both would use the Settings list and keep the same tracks.
func TestStepLanguagesGiveEachWorkflowItsOwnTracks(t *testing.T) {
	info := withAudio(media.Stream{Codec: "aac", Language: "eng"}, media.Stream{Codec: "aac", Language: "deu"},
		media.Stream{Codec: "aac", Language: "jpn"})
	newSel := func(audio ...string) Selectors {
		s := DefaultSelectors(3)
		s.Audio, s.OnlyMyLanguages, _ = StandardAudio("my_languages", true)
		s.FileType = "keep"
		s.AudioLangs = audio
		return s
	}
	const settings = "deu,jpn"
	english := stepPlan(t, newSel("eng"), settings, info)
	if d := dropped(english); len(d) != 2 || d[0] != 2 || d[1] != 3 {
		t.Errorf("english workflow drops %v, want [2 3] (keeps eng only)", d)
	}
	german := stepPlan(t, newSel("ger"), settings, info)
	if d := dropped(german); len(d) != 2 || d[0] != 1 || d[1] != 3 {
		t.Errorf("german workflow drops %v, want [1 3] (keeps deu only)", d)
	}
	// no list on the step: the Settings list, as before
	if d := dropped(stepPlan(t, newSel(), settings, info)); len(d) != 1 || d[0] != 1 {
		t.Errorf("no step list drops %v, want [1] (Settings deu,jpn)", d)
	}
}

// Subtitles with no list of their own use the Audio step's list, then Settings (G2).
func TestSubtitleLanguagesFallBack(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "eng"},
		media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "fre"},
		media.Stream{Index: 3, Type: "subtitle", Codec: "subrip", SubKind: "text", Language: "jpn"})
	subs := func(audio, own []string) Selectors {
		s := DefaultSelectors(3)
		s.Subtitles = "my_languages"
		s.AudioLangs, s.SubLangs = audio, own
		return s
	}
	// subtitles' own list wins over the Audio step's
	if d := dropped(stepPlan(t, subs([]string{"jpn"}, []string{"fre"}), "eng", info)); len(d) != 2 || d[0] != 1 || d[1] != 3 {
		t.Errorf("own list drops %v, want [1 3]", d)
	}
	// no own list: the Audio step's
	if d := dropped(stepPlan(t, subs([]string{"jpn"}, nil), "eng", info)); len(d) != 2 || d[0] != 1 || d[1] != 2 {
		t.Errorf("audio fallback drops %v, want [1 2]", d)
	}
	// neither: Settings
	if d := dropped(stepPlan(t, subs(nil, nil), "eng", info)); len(d) != 2 || d[0] != 2 || d[1] != 3 {
		t.Errorf("settings fallback drops %v, want [2 3]", d)
	}
}

// An empty list is the same as no list: the Settings list decides, so an existing flow's output is unchanged.
func TestEmptyStepLanguagesAreSettings(t *testing.T) {
	info := withAudio(media.Stream{Codec: "aac", Language: "eng"}, media.Stream{Codec: "aac", Language: "deu"})
	s := DefaultSelectors(3)
	s.Audio, s.OnlyMyLanguages, _ = StandardAudio("my_languages", true)
	s.FileType = "keep"
	base := dropped(stepPlan(t, s, "deu", info))
	s.AudioLangs = []string{}
	if got := dropped(stepPlan(t, s, "deu", info)); len(got) != len(base) || got[0] != base[0] {
		t.Errorf("empty list drops %v, Settings-only drops %v", got, base)
	}
}

// Bad codes and more than MaxStepLanguages are refused, with the same "Nothing was changed." shape.
func TestStepLanguagesValidation(t *testing.T) {
	valid := DefaultSelectors(3)
	valid.AudioLangs = []string{"eng"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid list refused: %v", err)
	}
	bad := DefaultSelectors(3)
	bad.AudioLangs = []string{"eng", "xyz"}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "Nothing was changed.") {
		t.Errorf("bad code: %v", err)
	}
	bad = DefaultSelectors(3)
	bad.SubLangs = []string{"xyz"}
	if err := bad.Validate(); err == nil {
		t.Error("bad subtitle code accepted")
	}
	many := DefaultSelectors(3)
	many.AudioLangs = make([]string, MaxStepLanguages+1)
	for i := range many.AudioLangs {
		many.AudioLangs[i] = "eng"
	}
	if err := many.Validate(); err == nil {
		t.Errorf("%d codes accepted", MaxStepLanguages+1)
	}
	many.AudioLangs = many.AudioLangs[:MaxStepLanguages]
	if err := many.Validate(); err != nil {
		t.Errorf("%d codes refused: %v", MaxStepLanguages, err)
	}
}

// Preview and run agree (G2): a saved flow is stored as JSON and compiled again on a run, so the plan a
// preview shows must match the plan from the stored template, with the step lists in it.
func TestPreviewAndRunAgreeOnStepLists(t *testing.T) {
	info := withAudio(media.Stream{Codec: "aac", Language: "eng"}, media.Stream{Codec: "aac", Language: "deu"},
		media.Stream{Codec: "aac", Language: "jpn"})
	s := DefaultSelectors(3)
	s.Audio, s.OnlyMyLanguages, _ = StandardAudio("my_languages", true)
	s.FileType = "keep"
	s.AudioLangs = []string{"eng"}
	const settings = "deu,jpn"
	preview := stepPlan(t, s, settings, info)

	b, err := json.Marshal(s.Template())
	if err != nil {
		t.Fatal(err)
	}
	var stored Template
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Languages = settings
	run := stored.Compile(info)
	if strings.Join(stringsOf(dropped(preview)), ",") != strings.Join(stringsOf(dropped(run)), ",") {
		t.Errorf("preview drops %v, run drops %v: they must agree", dropped(preview), dropped(run))
	}
}

func stringsOf(ints []int) []string {
	out := make([]string, len(ints))
	for i, n := range ints {
		out[i] = strconv.Itoa(n)
	}
	return out
}
