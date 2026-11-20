// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import "testing"

// W11 engine half: what the editor may do with a step's place, and which step types it may add, come
// with the check, so the page doesn't copy the compile rules. Until W10 the steps that convert keep
// their place and can't be added; a question, an action or an end (FB1b) goes before them.
func TestValidateSaysWhereEachStepAndTypeStands(t *testing.T) {
	d := Preset()
	r := Validate(d)
	if len(r.StepPlaces) != len(d.Nodes) {
		t.Errorf("%d places for %d nodes", len(r.StepPlaces), len(d.Nodes))
	}
	for _, n := range d.Nodes {
		p, ok := r.StepPlaces[n.ID]
		if !ok || !p.FixedPlace || p.FixedReason != "The steps that convert run in a set order for now, so they can't be moved or deleted. To skip this step, turn it off." {
			t.Errorf("%s: %+v", n.ID, p)
		}
	}
	defs := Definitions()
	if len(r.AddSteps) != len(defs) {
		t.Errorf("%d add rules for %d types", len(r.AddSteps), len(defs))
	}
	for _, def := range defs {
		a, ok := r.AddSteps[def.Type]
		switch {
		case !ok:
			t.Errorf("%s: no add rule", def.Type)
		case IsQuestion(def.Type) || IsAction(def.Type):
			if !a.Addable || a.After != d.Nodes[0].ID || a.Reason != "" {
				t.Errorf("%s: %+v (want addable after Library files)", def.Type, a)
			}
		case IsEnd(def.Type):
			if a.Addable || a.Reason == "" {
				t.Errorf("%s: %+v (an end is placed in the graph)", def.Type, a)
			}
		case def.ReadOnly:
			if a.Addable || a.Reason == "" {
				t.Errorf("%s: %+v", def.Type, a)
			}
		default:
			if a.Addable || a.Reason != "Can't run among the steps that convert yet." || a.After != "" {
				t.Errorf("%s: %+v", def.Type, a)
			}
		}
	}

	// a doc this version can't read still answers with {}: the maps are there, empty, not null
	bad := Validate(Doc{SchemaVersion: SchemaVersion + 1})
	if len(bad.Issues) != 1 || bad.Issues[0].Code != "unknown_schema_version" {
		t.Fatalf("issues %+v", bad.Issues)
	}
	if bad.StepPlaces == nil || bad.AddSteps == nil || len(bad.StepPlaces) != 0 || len(bad.AddSteps) != 0 {
		t.Errorf("unknown version: places %v, adds %v", bad.StepPlaces, bad.AddSteps)
	}
}
