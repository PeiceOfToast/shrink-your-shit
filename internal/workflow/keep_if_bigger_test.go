// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R8: the Save the result step's "ifBigger" setting. A new workflow keeps the original when the new file is
// bigger; a stored step without the field replaces it, as it always did, and reads back saying so.
func TestSaveResultIfBiggerSchema(t *testing.T) {
	def, ok := Definition(TypeCommit)
	if !ok {
		t.Fatal("no definition for Save the result")
	}
	b, err := json.Marshal(def.ConfigSchema)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties map[string]struct {
			Default any      `json:"default"`
			Enum    []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	p, ok := s.Properties["ifBigger"]
	if !ok {
		t.Fatalf("no ifBigger in the schema: %s", b)
	}
	if p.Default != "keep" || len(p.Enum) != 2 || p.Enum[0] != "keep" || p.Enum[1] != "replace" {
		t.Errorf("ifBigger: default %v, choices %v; want keep, then keep and replace", p.Default, p.Enum)
	}
}

func commitOf(d Doc) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeCommit {
			return &d.Nodes[i]
		}
	}
	return nil
}

func withCommit(config map[string]any) Doc {
	d := FromTemplate(rules.Template{Kind: rules.KindSelectors, Selectors: ptr(rules.DefaultSelectors(3))}, nil)
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeCommit {
			d.Nodes[i].Config = config
		}
	}
	return d
}

func TestNewWorkflowsKeepByDefault(t *testing.T) {
	p := Preset()
	if c := commitOf(p); c == nil || c.Config["ifBigger"] != "keep" {
		t.Fatalf("a new workflow's Save the result: %v, want ifBigger keep", c)
	}
	tmpl, issues := ToTemplate(p, rules.Template{})
	if len(issues) != 0 || tmpl.Selectors == nil || !tmpl.Selectors.KeepIfBigger {
		t.Errorf("preset: keep %v, issues %+v; want KeepIfBigger and no issues", tmpl.Selectors != nil && tmpl.Selectors.KeepIfBigger, issues)
	}
	// and it survives a save and a read back
	back := roundTrip(t, p)
	tmpl, issues = ToTemplate(back, rules.Template{})
	if len(issues) != 0 || tmpl.Selectors == nil || !tmpl.Selectors.KeepIfBigger {
		t.Errorf("after a round trip: issues %+v", issues)
	}
}

// An existing workflow has no "ifBigger": it behaves as replace, and FillCommitDefaults makes that explicit
// when it is read back.
func TestExistingWorkflowReadsBackAsReplace(t *testing.T) {
	old := withCommit(map[string]any{"policy": "replace"})
	tmpl, issues := ToTemplate(old, rules.Template{})
	if len(issues) != 0 || tmpl.Selectors == nil || tmpl.Selectors.KeepIfBigger {
		t.Fatalf("an old workflow: issues %+v, keep %v; want replace", issues, tmpl.Selectors != nil && tmpl.Selectors.KeepIfBigger)
	}

	FillCommitDefaults(&old)
	if c := commitOf(old); c == nil || c.Config["ifBigger"] != "replace" {
		t.Errorf("read back: %v, want ifBigger replace", commitOf(old).Config)
	}
	// a step that already says keep keeps it
	kept := withCommit(map[string]any{"policy": "beside", "ifBigger": "keep"})
	FillCommitDefaults(&kept)
	if c := commitOf(kept); c.Config["ifBigger"] != "keep" {
		t.Errorf("a keep step was changed to %v", c.Config["ifBigger"])
	}
	// and a step with no config map at all gets one
	bare := withCommit(nil)
	FillCommitDefaults(&bare)
	if c := commitOf(bare); c.Config["ifBigger"] != "replace" {
		t.Errorf("a step with no config: %v", c.Config)
	}
}

func TestIfBiggerReplaceAndInvalid(t *testing.T) {
	tmpl, issues := ToTemplate(withCommit(map[string]any{"policy": "beside", "ifBigger": "replace"}), rules.Template{})
	if len(issues) != 0 || tmpl.Selectors.KeepIfBigger {
		t.Errorf("explicit replace: issues %+v", issues)
	}
	_, issues = ToTemplate(withCommit(map[string]any{"policy": "replace", "ifBigger": "maybe"}), rules.Template{})
	found := false
	for _, i := range issues {
		if i.Code == "invalid_config" && i.Field == "ifBigger" {
			found = true
		}
	}
	if !found {
		t.Errorf("an unknown ifBigger: %+v, want an invalid_config on ifBigger", issues)
	}
}
