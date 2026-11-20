// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
	"testing"
)

// A workflow's own variables: a name a person may type, not a built-in, and two variables differ
// (W4a, design-joblog-vars section 3).
func TestCheckVariables(t *testing.T) {
	good := Doc{Variables: []Variable{{Name: "movie_name"}, {Name: "season"}, {Name: "a1"}, {Name: strings.Repeat("a", 40)}}}
	if got := CheckVariables(good); len(got) != 0 {
		t.Fatalf("names a person may type were refused: %+v", got)
	}
	for _, c := range []struct {
		why  string
		v    Variable
		code string
	}{
		{"a capital", Variable{Name: "Title"}, "invalid_variable_name"},
		{"a number first", Variable{Name: "2fast"}, "invalid_variable_name"},
		{"a dash", Variable{Name: "my-name"}, "invalid_variable_name"},
		{"a space", Variable{Name: "my name"}, "invalid_variable_name"},
		{"empty", Variable{Name: ""}, "invalid_variable_name"},
		{"41 characters", Variable{Name: strings.Repeat("a", 41)}, "invalid_variable_name"},
		{"a built-in", Variable{Name: "title"}, "builtin_variable_name"},
		{"a field-only built-in", Variable{Name: "sub_lang"}, "builtin_variable_name"},
	} {
		got := CheckVariables(Doc{Variables: []Variable{c.v}})
		if len(got) != 1 || got[0].Code != c.code || got[0].Name != c.v.Name || got[0].Severity != "error" {
			t.Errorf("%s: got %+v, want one %s about %q", c.why, got, c.code, c.v.Name)
		}
	}
	twice := CheckVariables(Doc{Variables: []Variable{{Name: "mine"}, {Name: "mine"}}})
	if len(twice) != 1 || twice[0].Code != "duplicate_variable" || twice[0].Name != "mine" {
		t.Fatalf("two variables with one name: %+v", twice)
	}
}

// Validate carries the variable checks, so Publish's gate and the Validate endpoint refuse them too.
func TestValidateRefusesABuiltInVariableName(t *testing.T) {
	d := Preset()
	d.Variables = []Variable{{Name: "title", Type: "string"}}
	r := Validate(d)
	if !r.Errors() {
		t.Fatal("a variable named like a built-in did not block Publish")
	}
	if !slices.ContainsFunc(r.Issues, func(i Issue) bool {
		return i.Code == "builtin_variable_name" && i.Name == "title" && i.Severity == "error"
	}) {
		t.Fatalf("no builtin_variable_name issue: %+v", r.Issues)
	}
	d.Variables = nil
	if r := Validate(d); r.Errors() {
		t.Fatalf("the preset itself does not validate: %+v", r.Issues)
	}
}
