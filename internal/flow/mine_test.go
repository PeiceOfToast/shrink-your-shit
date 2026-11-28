// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
	"strings"
	"testing"
)

// audioOf reads the audio step's flag and lists out of a flow.
func audioOf(t *testing.T, f Flow) (all bool, audio, subs []string) {
	t.Helper()
	for _, st := range f.Steps {
		if st.Type != "audio.tracks" {
			continue
		}
		var s struct {
			AllLanguages      *bool    `json:"allLanguages"`
			Languages         []string `json:"languages"`
			SubtitleLanguages []string `json:"subtitleLanguages"`
		}
		if err := json.Unmarshal(st.Settings, &s); err != nil {
			t.Fatal(err)
		}
		return s.AllLanguages != nil && *s.AllLanguages, s.Languages, s.SubtitleLanguages
	}
	t.Fatal("no audio step")
	return false, nil, nil
}

func audioFlow(settings string) Flow {
	return Flow{Steps: []StepRef{{Type: "audio.tracks", Settings: json.RawMessage(settings)}}}
}

// With "keep every language" off and an empty list, a run keeps the person's languages as they are when it starts.
func TestMyLanguagesAreLookedUpAtTheRun(t *testing.T) {
	got := ResolveMyLanguages(audioFlow(`{"allLanguages":false,"languages":[]}`), []string{"fra", "ita"})
	_, audio, _ := audioOf(t, got)
	if len(audio) != 2 || audio[0] != "fra" || audio[1] != "ita" {
		t.Errorf("audio = %v, want [fra ita]", audio)
	}
}

// "Keep every language" is never filled in, and an older flow with no flag keeps its meaning (empty is every language).
func TestAllLanguagesAndOlderFlowsAreUntouched(t *testing.T) {
	for _, in := range []string{`{"allLanguages":true,"languages":[]}`, `{"languages":[]}`} {
		got := ResolveMyLanguages(audioFlow(in), []string{"fra"})
		_, audio, _ := audioOf(t, got)
		if len(audio) != 0 {
			t.Errorf("%s: audio = %v, want none (every language)", in, audio)
		}
	}
}

// The chip "Keep only my languages" is stored as not-all with an empty list, not as a token.
func TestMyLanguagesChoiceIsNotAllAndEmpty(t *testing.T) {
	a := DefaultAnswers()
	a["allLanguages"] = false
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	all, audio, _ := audioOf(t, f)
	if all || len(audio) != 0 {
		t.Errorf("all = %v, audio = %v; want not-all with an empty list", all, audio)
	}
}

// "Use my languages everywhere" with no list of its own is my languages: an empty list, looked up at the run, with
// nothing copied from the settings at save.
func TestEverywhereWithNoListIsMyLanguages(t *testing.T) {
	a := DefaultAnswers()
	a["allLanguages"] = false
	a["languagesEverywhere"] = true
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if _, audio, _ := audioOf(t, f); len(audio) != 0 {
		t.Fatalf("saved audio = %v, want an empty list", audio)
	}
	_, audio, _ := audioOf(t, ResolveMyLanguages(f, []string{"fra", "ita"}))
	if len(audio) != 2 || audio[0] != "fra" || audio[1] != "ita" {
		t.Errorf("run audio = %v, want [fra ita]", audio)
	}
}

// Fact 1: the flow keeps "keep every language" off with an explicit empty list, not coerced to every language.
func TestFlowKeepsNotAllWithAnExplicitEmptyList(t *testing.T) {
	a := DefaultAnswers()
	a["allLanguages"] = false
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for _, st := range f.Steps {
		if st.Type != "audio.tracks" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(st.Settings, &raw); err != nil {
			t.Fatal(err)
		}
		if string(raw["allLanguages"]) != "false" || string(raw["languages"]) != "[]" {
			t.Errorf("audio settings carry allLanguages=%s languages=%s; want false and []", raw["allLanguages"], raw["languages"])
		}
		return
	}
	t.Fatal("no audio step")
}

// Fact 2: a flow's answers round-trip through FromAnswers and ToAnswers: not-all with a list, and not-all with none.
func TestAnswersRoundTripThroughTheFlow(t *testing.T) {
	for name, set := range map[string]Answers{
		"my languages, none picked": {"allLanguages": false, "languages": []string{}},
		"a list of its own":         {"allLanguages": false, "languages": []string{"fra", "ita"}},
		"every language":            {"allLanguages": true},
	} {
		a := DefaultAnswers()
		for k, v := range set {
			a[k] = v
		}
		f, problems := FromAnswers(a, DefaultAnswers())
		if len(problems) > 0 {
			t.Fatalf("%s: problems: %v", name, problems)
		}
		back, ok := ToAnswers(f)
		if !ok {
			t.Fatalf("%s: the flow does not fit the guide", name)
		}
		if back["allLanguages"] != a["allLanguages"] {
			t.Errorf("%s: allLanguages came back %v, want %v", name, back["allLanguages"], a["allLanguages"])
		}
		if !sameStrings(back["languages"], a["languages"]) {
			t.Errorf("%s: languages came back %v, want %v", name, back["languages"], a["languages"])
		}
	}
}

// Fact 3: the preview says "your languages" for not-all with no list, and "every language" only for all.
func TestPreviewSaysYourLanguagesWhenNoneIsPicked(t *testing.T) {
	a := DefaultAnswers()
	a["allLanguages"] = false
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	var audio string
	for _, s := range Sentences(f) {
		if len(s) >= 6 && s[:6] == "Audio:" {
			audio = s
		}
	}
	if audio == "" || !strings.Contains(audio, "your languages") || strings.Contains(audio, "every language") {
		t.Errorf("audio sentence = %q, want it to say your languages", audio)
	}
}

func sameStrings(a, b any) bool {
	x, _ := a.([]string)
	y, _ := b.([]string)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
