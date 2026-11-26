// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// selectorsBody is a Standard selectors body with the given fields and quality 3.
func selectorsBody(t *testing.T, fields map[string]any) map[string]json.RawMessage {
	t.Helper()
	fields["quality"] = 3
	out := map[string]json.RawMessage{}
	for k, v := range fields {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = b
	}
	return out
}

func selectorCode(err error) string {
	var se *rules.SelectorError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// The refusal: "my languages" needs a list, and a step's own list (G2) counts as one.
func TestLanguagesNeededOnlyWithNoListAnywhere(t *testing.T) {
	asIsMine := map[string]any{"audio": "as_is", "onlyMyLanguages": true, "subtitles": "keep_all", "fileType": "keep"}

	if _, err := readSelectors(selectorsBody(t, asIsMine), nil, nil); selectorCode(err) != "languages_needed" {
		t.Errorf("audio my languages, no list anywhere: want languages_needed, got %v", err)
	}
	if _, err := readSelectors(selectorsBody(t, asIsMine), []string{"eng"}, nil); err != nil {
		t.Errorf("the person's languages answer it: %v", err)
	}
	kept := &rules.Selectors{AudioLangs: []string{"fre"}}
	if _, err := readSelectors(selectorsBody(t, asIsMine), nil, kept); err != nil {
		t.Errorf("the audio step's own list answers it, with no person's languages: %v", err)
	}

	subsMine := map[string]any{"audio": "keep_all", "subtitles": "my_languages", "fileType": "keep"}
	if _, err := readSelectors(selectorsBody(t, subsMine), nil, nil); selectorCode(err) != "languages_needed" {
		t.Errorf("subtitles my languages, no list anywhere: want languages_needed, got %v", err)
	}
	if _, err := readSelectors(selectorsBody(t, subsMine), nil, &rules.Selectors{AudioLangs: []string{"ger"}}); err != nil {
		t.Errorf("subtitles fall back to the audio step's list: %v", err)
	}
	if _, err := readSelectors(selectorsBody(t, subsMine), nil, &rules.Selectors{SubLangs: []string{"ita"}}); err != nil {
		t.Errorf("subtitles' own list answers it: %v", err)
	}
}

// The page never sets the step lists: they come back from the stored choice, unchanged by a save.
func TestStepLanguagesAreKeptAcrossASave(t *testing.T) {
	kept := &rules.Selectors{AudioLangs: []string{"jpn"}, SubLangs: []string{"eng", "fre"}}
	s, err := readSelectors(selectorsBody(t, map[string]any{"audio": "keep_all", "subtitles": "keep_all", "fileType": "keep"}), nil, kept)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if strings.Join(s.AudioLangs, ",") != "jpn" || strings.Join(s.SubLangs, ",") != "eng,fre" {
		t.Errorf("step lists lost on save: audio %v subs %v", s.AudioLangs, s.SubLangs)
	}
}

// The page cannot set a step list: a changed list is refused (409, the same as the other echoed fields the
// page cannot set), and an echo of the stored list is not a change.
func TestPageCannotSetAStepList(t *testing.T) {
	stored := &rules.Selectors{AudioLangs: []string{"jpn"}, SubLangs: []string{"eng", "fre"}}
	changed := selectorsBody(t, map[string]any{"audioLanguages": []string{"eng"}})
	if changedEcho(changed, stored) != echoLanguages {
		t.Error("a changed audioLanguages list must count as a change")
	}
	echo := selectorsBody(t, map[string]any{"audioLanguages": []string{"jpn"}, "subtitleLanguages": []string{"eng", "fre"}})
	if changedEcho(echo, stored) != "" {
		t.Error("an echo of the stored lists is not a change")
	}
	if changedEcho(selectorsBody(t, map[string]any{"subtitleLanguages": []string{"fre"}}), stored) != echoLanguages {
		t.Error("a changed subtitleLanguages list must count as a change")
	}
}

// Refusals on a kept list: more than MaxStepLanguages, and a bad code.
func TestStepLanguagesValidationRefuses(t *testing.T) {
	ok := selectorsBody(t, map[string]any{"audio": "keep_all", "subtitles": "keep_all", "fileType": "keep"})

	many := make([]string, rules.MaxStepLanguages+1)
	for i := range many {
		many[i] = "eng"
	}
	if _, err := readSelectors(ok, nil, &rules.Selectors{AudioLangs: many}); selectorCode(err) != "invalid" {
		t.Errorf("21 audio languages: want invalid, got %v", err)
	}
	if _, err := readSelectors(ok, nil, &rules.Selectors{SubLangs: []string{"not-a-code!"}}); selectorCode(err) != "invalid" {
		t.Errorf("bad subtitle code: want invalid, got %v", err)
	}
}
