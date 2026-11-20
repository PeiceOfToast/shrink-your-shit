// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"
)

// templatedFields are the (step type, setting) pairs that take {{name}} variables, in the order the
// registry lists them: the design's four (design-joblog-vars section 3) and FB1b's three run-card fields.
func templatedFields() []string {
	var out []string
	for _, def := range Definitions() {
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		for k, s := range props {
			if p, ok := s.(map[string]any); ok && p["x-variables"] == true {
				out = append(out, def.Type+"/"+k)
			}
		}
	}
	return out
}

// The flag the editor reads must be on exactly the fields that take variables, and each one's help
// text must show an example (review2-504 P3b: the description had no test).
func TestTemplatedFieldsAndTheirHelpText(t *testing.T) {
	got := map[string]bool{}
	for _, f := range templatedFields() {
		got[f] = true
	}
	want := map[string]string{ // the field: a piece of its description that shows an example
		"output.commit/folder":     "{{library}}",
		"output.commit/outputName": "{{title}}",
		"output.commit/srtName":    "{{sub_lang}}",
		"output.commit/trackTitle": "{{resolution}}",
		// FB1b: the run cards' templated fields
		"check.variable/value":      "{{resolution}}",
		"action.set_variable/value": "{{title}}",
		"action.note/text":          "{{title}}",
	}
	if len(got) != len(want) {
		t.Errorf("fields with x-variables: %v, want %d", templatedFields(), len(want))
	}
	for f := range want {
		if !got[f] {
			t.Errorf("%s does not take variables (x-variables is missing)", f)
		}
	}
	for _, def := range Definitions() {
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		for k, s := range props {
			p, _ := s.(map[string]any)
			example, isTemplated := want[def.Type+"/"+k]
			if p["x-variables"] != true {
				continue // extras are caught by the count above
			}
			if !isTemplated {
				t.Errorf("%s/%s takes variables but is not in the list", def.Type, k)
				continue
			}
			if d, _ := p["description"].(string); !strings.Contains(d, example) {
				t.Errorf("%s/%s: its help text %q should show an example like %s", def.Type, k, d, example)
			}
		}
	}
}

func templateDoc(t *testing.T, variables []Variable, config map[string]any) Doc {
	t.Helper()
	d := withCommit(config)
	d.Variables = variables
	return d
}

// A template name must be one the workflow has: an unknown one is refused with the field, the name and
// a suggested one; an unclosed {{ is refused as such; the field-only names exist in the .srt name only.
func TestCheckTemplates(t *testing.T) {
	own := []Variable{{Name: "quality"}}
	for _, c := range []struct {
		name   string
		config map[string]any
		code   string
		field  string
		ref    string
		sugest string
	}{
		{"a known built-in", map[string]any{"outputName": "{{title}} (smaller)"}, "", "", "", ""},
		{"the workflow's own variable", map[string]any{"outputName": "{{quality}}-{{title}}"}, "", "", "", ""},
		{"an unknown name, a typo", map[string]any{"outputName": "{{titel}}"}, "unknown_variable", "outputName", "titel", "title"},
		{"an unknown name, no close one", map[string]any{"trackTitle": "{{whatever}}"}, "unknown_variable", "trackTitle", "whatever", ""},
		{"unclosed", map[string]any{"outputName": "{{title (smaller)"}, "unclosed_variable", "outputName", "", ""},
		{"sub_lang in the .srt name", map[string]any{"srtName": "{{title}}.{{sub_lang}}{{sub_kind}}.srt"}, "", "", "", ""},
		{"sub_lang outside the .srt name", map[string]any{"outputName": "{{sub_lang}}.mkv"}, "unknown_variable", "outputName", "sub_lang", ""},
		{"the folder is a template too", map[string]any{"folder": "/media/{{library}}"}, "", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := templateDoc(t, own, c.config)
			got := CheckTemplates(d)
			if c.code == "" {
				if len(got) != 0 {
					t.Fatalf("issues %+v, want none", got)
				}
				if r := Validate(d); r.Errors() {
					t.Fatalf("Validate refuses: %+v", r.Issues)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("no issue, want %s on %s", c.code, c.field)
			}
			i := got[0]
			if i.Code != c.code || i.Severity != "error" || i.Field != c.field || i.Name != c.ref || i.Suggest != c.sugest {
				t.Errorf("%+v, want code %s field %s name %q suggest %q", i, c.code, c.field, c.ref, c.sugest)
			}
			if i.Message == "" || i.NodeID == "" {
				t.Errorf("%+v: the message and the step must be there for the page", i)
			}
			if !Validate(d).Errors() {
				t.Error("Validate must refuse it: Publish keys on Errors()")
			}
		})
	}
}
