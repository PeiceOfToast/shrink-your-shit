// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"maps"
	"testing"
)

// #694: the file's state on each port follows where the step sits. A step on the walk after the save
// has its file saved on every port, even a question the definition says is original; a step before the
// save keeps the definition's values.
func TestFileOnFollowsThePositionOfTheStep(t *testing.T) {
	d, by := runDoc()
	save := d.Nodes[len(d.Nodes)-1]
	cmp := node(TypeCheckResult, "Worth it?", map[string]any{"test": "saved_at_least", "percent": float64(30)})
	d.Nodes = append(d.Nodes, cmp)
	d.Edges = append(d.Edges, link(save.ID, "success", cmp.ID)) // Yes and No unlinked
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if got := r.FileOn[cmp.ID]; len(got) != 2 || got["match"] != FileSaved || got["else"] != FileSaved {
		t.Errorf("compare after the save: fileOn %v, want match and else saved", got)
	}
	if def := mustDef(t, TypeCheckResult); def.FileOn["match"] != FileOriginal {
		t.Errorf("the definition changed: %v", def.FileOn)
	}

	// before the save: the question keeps the definition's values
	q := by["q"]
	if got, want := r.FileOn[q.ID], mustDef(t, TypeCheckName).FileOn; !maps.Equal(got, want) {
		t.Errorf("check before the save: fileOn %v, want the definition's %v", got, want)
	}

	// an after-save action is saved on all its ports
	tag := node(TypeNotify, "", map[string]any{"text": "Done {{title}}"})
	d.Nodes = append(d.Nodes, tag)
	d.Edges = append(d.Edges, link(save.ID, "success", tag.ID))
	r = Validate(d)
	outs := mustDef(t, TypeNotify).Outputs
	if len(outs) == 0 {
		t.Fatal("a notification has no output ports to check")
	}
	for _, p := range outs {
		if r.FileOn[tag.ID][p.Name] != FileSaved {
			t.Errorf("after-save %s port %s: %q, want saved", TypeNotify, p.Name, r.FileOn[tag.ID][p.Name])
		}
	}
}

func mustDef(t *testing.T, typ string) StepDefinition {
	t.Helper()
	def, ok := Definition(typ)
	if !ok {
		t.Fatalf("no definition for %s", typ)
	}
	return def
}
