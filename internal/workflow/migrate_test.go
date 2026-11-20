// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"fmt"
	"testing"
)

// sampleDocSteps are test-only upgrades, registered here and nowhere else: 1 to 2 and 2 to 3, each leaving
// its mark in "trail" so the test can see the order they ran in.
func sampleDocSteps() []DocMigration {
	mark := func(from int) func(map[string]any) error {
		return func(m map[string]any) error {
			trail, _ := m["trail"].([]any)
			m["trail"] = append(trail, fmt.Sprintf("%d>%d", from, from+1))
			return nil
		}
	}
	return []DocMigration{{From: 1, Run: mark(1)}, {From: 2, Run: mark(2)}}
}

func sampleConfigSteps() []ConfigMigration {
	return []ConfigMigration{
		{From: 1, Run: func(c map[string]any) error { c["renamed"] = c["old"]; delete(c, "old"); return nil }},
		{From: 2, Run: func(c map[string]any) error { c["units"] = "percent"; return nil }},
	}
}

func TestDocChainRunsInOrderToTheCurrentVersion(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"nodes":[],"edges":[],"variables":[]}`)
	var m map[string]any
	json.Unmarshal(raw, &m)
	if err := upgradeDoc(m, 3, sampleDocSteps()); err != nil {
		t.Fatal(err)
	}
	trail, _ := m["trail"].([]any)
	if fmt.Sprint(trail) != "[1>2 2>3]" || m["schemaVersion"] != float64(3) {
		t.Fatalf("trail %v, schemaVersion %v: want the steps 1>2 then 2>3 and version 3", trail, m["schemaVersion"])
	}
}

func TestDocWithNoUpgradePathIsLeftAsStored(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(`{"schemaVersion":1}`), &m)
	if err := upgradeDoc(m, 3, sampleDocSteps()[:1]); err != nil { // no 2>3 step
		t.Fatal(err)
	}
	if _, ok := m["trail"]; !ok || m["schemaVersion"] == float64(3) {
		t.Fatalf("a missing step must not claim the current version: %v", m)
	}
}

func TestNewerDocumentIsNotTouched(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(`{"schemaVersion":4}`), &m)
	if err := upgradeDoc(m, 3, sampleDocSteps()); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["trail"]; ok || m["schemaVersion"] != float64(4) {
		t.Fatalf("a newer document must be left alone: %v", m)
	}
	d, err := load([]byte(`{"schemaVersion":4}`), SchemaVersion, nil, nil, Definitions())
	if err != nil {
		t.Fatal(err)
	}
	if got := codesOf(Validate(d)); len(got) != 1 || got[0] != "unknown_schema_version" {
		t.Fatalf("newer schema: codes %v, want unknown_schema_version", got)
	}
}

func TestConfigChainUpgradesAnOlderStep(t *testing.T) {
	n := Node{ID: "a", Type: "x", TypeVersion: 1, Config: map[string]any{"old": 7}}
	upgradeNode(&n, 3, sampleConfigSteps())
	if n.TypeVersion != 3 || n.Config["renamed"] != 7 || n.Config["units"] != "percent" || n.Config["old"] != nil {
		t.Fatalf("node %+v: want typeVersion 3 with the 1>2 rename and the 2>3 units", n)
	}
}

func TestConfigWithNoUpgradePathStaysAtItsVersion(t *testing.T) {
	n := Node{ID: "a", Type: "x", TypeVersion: 0}
	upgradeNode(&n, 3, sampleConfigSteps()[1:]) // no 0>1 step
	if n.TypeVersion != 0 {
		t.Fatalf("typeVersion %d: a node with no path must keep its version so Validate refuses it", n.TypeVersion)
	}
}

// A step made by a newer build is refused, and so is an older one this build can't upgrade.
func TestNewerStepVersionIsRefusedAndAnUnupgradableOneToo(t *testing.T) {
	d := Preset()
	d.Nodes[1].TypeVersion = 9
	if got := codesOf(Validate(d)); !contains(got, "unknown_type_version") {
		t.Fatalf("newer step: codes %v, want unknown_type_version", got)
	}
	d = Preset()
	d.Nodes[1].TypeVersion = 0
	if got := codesOf(Validate(d)); !contains(got, "unknown_type_version") {
		t.Fatalf("older step with no upgrade: codes %v, want unknown_type_version", got)
	}
}

func TestLoadLeavesCurrentStoredShapeUnchanged(t *testing.T) {
	raw, _ := json.Marshal(Preset())
	d, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	var plain Doc
	json.Unmarshal(raw, &plain)
	if canonical(t, d) != canonical(t, plain) {
		t.Fatalf("loading a current document changed it")
	}
}

func codesOf(r Result) []string {
	var out []string
	for _, i := range r.Issues {
		out = append(out, i.Code)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Load wires the chain in: a stored document with an older schema and an older step comes back at the current
// versions, upgraded in order, through the same load path the store uses.
func TestLoadRunsTheChainOnAStoredDocument(t *testing.T) {
	defs := Definitions()
	for i := range defs {
		if defs[i].Type == TypeConvert {
			defs[i].TypeVersion = 3
		}
	}
	raw := []byte(`{"schemaVersion":1,"nodes":[{"id":"c","type":"` + TypeConvert + `","typeVersion":1,"config":{"old":7},"enabled":true}],"edges":[],"variables":[]}`)
	d, err := load(raw, 3, sampleDocSteps(), map[string][]ConfigMigration{TypeConvert: sampleConfigSteps()}, defs)
	if err != nil {
		t.Fatal(err)
	}
	n := d.Nodes[0]
	if d.SchemaVersion != 3 || n.TypeVersion != 3 || n.Config["renamed"] != float64(7) || n.Config["units"] != "percent" {
		t.Fatalf("doc version %d, node %+v: want version 3 and both config steps", d.SchemaVersion, n)
	}
}
