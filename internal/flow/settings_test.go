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
