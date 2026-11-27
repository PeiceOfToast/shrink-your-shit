// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"strings"
	"testing"
)

type fakeSettings struct {
	Format string  `set:"format" label:"Format" choices:"a,b" default:"a"`
	Count  int     `set:"count" label:"Count" min:"1" max:"3" default:"2"`
	Flag   bool    `set:"flag" label:"Flag" default:"true"`
	Note   *string `set:"note" label:"Note"`
}

func fakeDef() Definition {
	return Definition{Type: "test.step", Version: 1, Settings: fakeSettings{}}
}

func TestDecodeSettingsDefaults(t *testing.T) {
	s, err := DecodeSettings(fakeDef(), nil)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	got := s.(fakeSettings)
	if got.Format != "a" || got.Count != 2 || !got.Flag || got.Note != nil {
		t.Errorf("defaults = %+v, want format a, count 2, flag true, note nil", got)
	}
}

func TestDecodeSettingsValues(t *testing.T) {
	s, err := DecodeSettings(fakeDef(), []byte(`{"format":"b","count":3,"flag":false,"note":"hello"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := s.(fakeSettings)
	if got.Format != "b" || got.Count != 3 || got.Flag || got.Note == nil || *got.Note != "hello" {
		t.Errorf("values = %+v, want the JSON's own", got)
	}
}

func TestDecodeSettingsRefusesUnknownField(t *testing.T) {
	_, err := DecodeSettings(fakeDef(), []byte(`{"wat":1}`))
	if err == nil || !strings.Contains(err.Error(), `"wat" is not a setting`) {
		t.Errorf("error = %v, want an unknown-field refusal", err)
	}
}

func TestDecodeSettingsRefusesWrongType(t *testing.T) {
	for _, raw := range []string{`{"count":"two"}`, `{"count":2.5}`, `{"flag":"yes"}`, `{"format":2}`} {
		if _, err := DecodeSettings(fakeDef(), []byte(raw)); err == nil {
			t.Errorf("%s: decoded, want a type refusal", raw)
		}
	}
}

func TestDecodeSettingsRefusesOutOfRange(t *testing.T) {
	cases := map[string]string{
		`{"count":0}`:    `Count must be at least 1.`,
		`{"count":4}`:    `Count must be at most 3.`,
		`{"format":"c"}`: `Format must be a or b.`,
	}
	for raw, want := range cases {
		_, err := DecodeSettings(fakeDef(), []byte(raw))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", raw, err, want)
		}
	}
}

func TestDecodeSettingsNullOnlyForOptional(t *testing.T) {
	if _, err := DecodeSettings(fakeDef(), []byte(`{"format":null}`)); err == nil ||
		!strings.Contains(err.Error(), `"format" cannot be empty`) {
		t.Errorf("null format: error = %v, want a refusal", err)
	}
	s, err := DecodeSettings(fakeDef(), []byte(`{"note":null}`))
	if err != nil {
		t.Fatalf("null note: %v", err)
	}
	if got := s.(fakeSettings); got.Note != nil {
		t.Errorf("note = %v, want nil", *got.Note)
	}
}

func TestTagProblemsRunsAgainOnHandBuiltSettings(t *testing.T) {
	problems := TagProblems(fakeDef(), fakeSettings{Format: "c", Count: 9})
	if len(problems) != 2 {
		t.Fatalf("problems = %+v, want the choice and the range", problems)
	}
	if problems[0].Field != "format" || problems[0].Code != "setting.choice" {
		t.Errorf("first = %+v, want the format choice", problems[0])
	}
	if problems[1].Field != "count" || problems[1].Code != "setting.range" {
		t.Errorf("second = %+v, want the count range", problems[1])
	}
	if problems := TagProblems(fakeDef(), fakeSettings{Format: "a", Count: 2}); len(problems) != 0 {
		t.Errorf("valid settings gave %+v", problems)
	}
}

// wideSettings carries the kinds page 07 needs besides text, whole number and yes/no: a number
// that can be unset, a list of text, choices whose words differ from their values, and whole
// numbers as choices.
type wideSettings struct {
	Default string   `set:"format" label:"Format" choices:"a=A, b=B" default:"b"`
	Note    *string  `set:"note" label:"Note" choices:"one,same=Same as this" unset:"No note"`
	Average *float64 `set:"average" label:"Average" min:"0.1" max:"10"`
	Names   []string `set:"names" label:"Names"`
	List    []string `set:"list" label:"List" default:"x,y"`
	Depth   *int     `set:"depth" label:"Depth" choices:"8=8-bit,10=10-bit"`
}

func wideDef() Definition {
	return Definition{Type: "test.wide", Version: 1, Settings: wideSettings{}}
}

func TestDecodeSettingsWideKinds(t *testing.T) {
	s, err := DecodeSettings(wideDef(), nil)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	got := s.(wideSettings)
	if got.Default != "b" {
		t.Errorf("format = %q, want the tag default", got.Default)
	}
	if got.Note != nil || got.Average != nil || got.Depth != nil {
		t.Errorf("optional fields = %+v, want nil", got)
	}
	if len(got.Names) != 0 || len(got.List) != 2 || got.List[0] != "x" || got.List[1] != "y" {
		t.Errorf("lists = %v / %v, want [] and [x y]", got.Names, got.List)
	}

	s, err = DecodeSettings(wideDef(), []byte(`{"note":"same","average":3.5,"names":["eng","ger"],"depth":10}`))
	if err != nil {
		t.Fatalf("decode values: %v", err)
	}
	got = s.(wideSettings)
	if got.Note == nil || *got.Note != "same" || got.Average == nil || *got.Average != 3.5 ||
		len(got.Names) != 2 || got.Depth == nil || *got.Depth != 10 {
		t.Errorf("values = %+v, want the JSON's own", got)
	}

	for _, raw := range []string{`{"average":"lots"}`, `{"names":"eng"}`, `{"depth":9}`} {
		if _, err := DecodeSettings(wideDef(), []byte(raw)); err == nil {
			t.Errorf("%s: decoded, want a refusal", raw)
		}
	}
	if _, err := DecodeSettings(wideDef(), []byte(`{"depth":9}`)); err != nil &&
		!strings.Contains(err.Error(), "Depth must be 8-bit or 10-bit.") {
		t.Errorf("depth 9: error = %v, want the choice sentence", err)
	}
	if _, err := DecodeSettings(wideDef(), []byte(`{"average":0.05}`)); err != nil &&
		!strings.Contains(err.Error(), "Average must be at least 0.1.") {
		t.Errorf("average 0.05: error = %v, want the range sentence", err)
	}
	if _, err := DecodeSettings(wideDef(), []byte(`{"average":12}`)); err == nil ||
		!strings.Contains(err.Error(), "Average must be at most 10.") {
		t.Errorf("average 12: error = %v, want the range sentence", err)
	}
}

// TestDecodeSettingsChoiceLabels checks the words the page gets for a choice whose value differs
// from its label, and the sentence that lists them.
func TestDecodeSettingsChoiceLabels(t *testing.T) {
	_, err := DecodeSettings(wideDef(), []byte(`{"note":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "Note must be one or Same as this.") {
		t.Errorf("error = %v, want the labels in the sentence", err)
	}
}
