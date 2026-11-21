// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
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

// A secret holds text (or nothing, or the sealed form the store keeps): a value of another shape would be
// stored in the clear, so it is refused wherever a variable is checked (W4b QA step 3).
func TestCheckVariablesRefusesASecretThatIsNotText(t *testing.T) {
	for _, c := range []struct {
		why string
		v   Variable
	}{
		{"an object", Variable{Name: "api_key", Type: "secret", Value: map[string]any{"k": "OBJSECRET"}}},
		{"a list", Variable{Name: "api_key", Type: "secret", Value: []any{"a", "b"}}},
		{"a number", Variable{Name: "api_key", Type: "secret", Value: 7}},
		{"an object beside a sealed value", Variable{Name: "api_key", Type: "secret", Value: map[string]any{"sealed": "AAAA", "nonce": "BBBB", "k": "x"}}},
	} {
		got := CheckVariables(Doc{Variables: []Variable{c.v}})
		if len(got) != 1 || got[0].Code != "invalid_secret_value" || got[0].Name != "api_key" || got[0].Severity != "error" {
			t.Errorf("%s: got %+v, want one invalid_secret_value about api_key", c.why, got)
		}
	}
	for _, c := range []struct {
		why string
		v   Variable
	}{
		{"text", Variable{Name: "api_key", Type: "secret", Value: "s3cret"}},
		{"no value", Variable{Name: "api_key", Type: "secret"}},
		{"null", Variable{Name: "api_key", Type: "secret", Value: nil, HasValue: true}},
		{"the store's sealed form", Variable{Name: "api_key", Type: "secret", Value: map[string]any{"sealed": "AAAA", "nonce": "BBBB"}}},
		{"text on a plain variable", Variable{Name: "studio", Type: "string", Value: "Mine"}},
	} {
		if got := CheckVariables(Doc{Variables: []Variable{c.v}}); len(got) != 0 {
			t.Errorf("%s was refused: %+v", c.why, got)
		}
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

// W2 (ui-shop's QA of #586): a server-wide variable is a name a {{name}} field may use: the check knows
// it in the live scopes, refuses a name nobody has, suggests a global for a near miss, and Validate alone
// (no scopes) still refuses it, so the test goes red on a build that forgets the scopes.
func TestTemplatesMayNameAGlobal(t *testing.T) {
	d := FromTemplate(rules.SliderTemplate(3), nil)
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeCommit {
			d.Nodes[i].Config["policy"], d.Nodes[i].Config["folderMode"], d.Nodes[i].Config["folder"] = "separate_folder", "one_folder", t.TempDir()+"/{{qa_sub}}"
		}
	}
	sc := Scopes{Global: map[string]Variable{"qa_sub": {Name: "qa_sub", Type: "string", Value: "alpha"}}, LeaveFile: true, Policy: Policy{CommandsAllowed: true}}
	if issues := CheckTemplatesWith(d, sc); len(issues) != 0 {
		t.Errorf("a global in a folder template: %+v", issues)
	}
	if res := ValidateWith(d, sc); res.Errors() {
		t.Errorf("validate with the globals: %+v", res.Issues)
	}
	if issues := CheckTemplates(d); len(issues) != 1 || issues[0].Code != "unknown_variable" {
		t.Errorf("without the scopes the name is unknown: %+v", issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeCommit {
			d.Nodes[i].Config["folder"] = t.TempDir()+"/{{qa_sun}}"
		}
	}
	if issues := CheckTemplatesWith(d, sc); len(issues) != 1 || issues[0].Code != "unknown_variable" || issues[0].Suggest != "qa_sub" {
		t.Errorf("a near miss suggests the global: %+v", issues)
	}
}
