// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// ui-shop 12:32Z: every step's config is checked against the schema the registry serves, and each
// problem names the step and the setting.
func TestConfigCheckedAgainstTheSchema(t *testing.T) {
	cases := []struct {
		name, field string
		value       any
		words       string
	}{
		{"a negative number", "minBitrateKbps", -5.0, "can't be less than 0"},
		{"above the maximum", "fps", 1200.0, "can't be more than 1000"},
		{"not in the choices", "resolution", 999.0, "999 is not a choice"},
		{"the wrong type", "fps", "fast", "wrong kind of value (a number)"},
		{"not a whole number", "size", 1.5, "wrong kind of value (a whole number)"},
		{"a list item not in the choices", "codec", []any{"hevc", "betamax"}, "betamax is not a choice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, check := branchDoc([]Node{{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{}}})
			for i := range d.Nodes {
				if d.Nodes[i].ID == check.ID {
					d.Nodes[i].Config[c.field] = c.value
				}
			}
			var got []Issue
			for _, i := range Validate(d).Issues {
				if i.Code == "invalid_config" {
					got = append(got, i)
				}
			}
			if len(got) != 1 || got[0].NodeID != check.ID || got[0].Field != c.field || got[0].Severity != "error" ||
				!strings.Contains(got[0].Message, "In 'Is it 4K?', ") || !strings.Contains(got[0].Message, c.words) {
				t.Errorf("%+v", got)
			}
		})
	}
	// another step's: the reason of "Leave the file alone" is at most 200 characters
	d, _ := branchDoc([]Node{{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": strings.Repeat("x", 201)}}})
	if r := Validate(d); !r.Errors() || !strings.Contains(r.Issues[0].Message, "at most 200 characters") || r.Issues[0].Field != "reason" {
		t.Errorf("%+v", r.Issues)
	}
	// a valid config: no problem
	d, _ = branchDoc(segment(rules.SliderTemplate(2)))
	if r := Validate(d); r.Errors() {
		t.Errorf("valid: %+v", r.Issues)
	}
}

// A required setting without a default must be there (none of today's steps has one: a made-up step).
func TestConfigRequiredWithoutDefault(t *testing.T) {
	def := StepDefinition{Type: "x.test", ConfigSchema: object(map[string]any{"folder": map[string]any{"type": "string", "title": "Folder"},
		"speed": map[string]any{"type": "string", "default": "normal"}}, []string{"folder", "speed"})}
	got := checkConfig(Node{ID: "n1", Name: "Copy", Config: map[string]any{}}, def)
	if len(got) != 1 || got[0].Field != "folder" || got[0].Message != "In 'Copy', “Folder” needs a value." {
		t.Errorf("%+v", got)
	}
}

// User review 11-12 item 1: VBR goes through a workflow (the Convert video step) and back; quality stays
// exactly today's template.
func TestVBRThroughAWorkflow(t *testing.T) {
	s := rules.DefaultSelectors(3)
	s.Ignore = []string{}
	s.Resolution, s.MaxBitrate, s.RateControl, s.VBRMaxKbps = "720", "medium", "vbr", 3000
	d := FromTemplate(s.Template(), nil)
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%+v", issues)
	}
	got, ok := tm.SelectorsView()
	if !ok || got.RateControl != "vbr" || got.VBRMaxKbps != 3000 {
		t.Errorf("%+v", got)
	}
	q := rules.DefaultSelectors(3)
	q.Ignore = []string{}
	qt, _ := ToTemplate(FromTemplate(q.Template(), nil), rules.Template{})
	if qv, _ := qt.SelectorsView(); qv.RateControl != "" || qv.VBRMaxKbps != 0 {
		t.Errorf("quality: %+v", qv)
	}
	for _, n := range FromTemplate(q.Template(), nil).Nodes {
		if _, has := n.Config["rateControl"]; has {
			t.Error("a quality workflow's Convert video step has no VBR keys")
		}
	}
}
