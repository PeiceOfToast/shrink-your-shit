// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"fmt"
)

// A stored workflow is upgraded when it is read, not when it is saved: a revision keeps the bytes it was
// published with, and Load gives the rest of the app this build's shape. Each upgrade moves one version
// forward (From to From+1) and runs in order. A version that has no upgrade path stays as it was, and
// Validate refuses it as it always has. A version newer than this build's is never touched, its nodes
// included. A current document's node is upgraded by its own typeVersion, so a step type that moved on is
// upgraded even when the document is current.

// DocMigration upgrades a whole stored document from version From to From+1. It runs on the raw JSON
// object, before the Doc struct is read, so a field the struct no longer has can still be moved.
type DocMigration struct {
	From int
	Run  func(doc map[string]any) error
}

// ConfigMigration upgrades one step's stored config from its type version From to From+1.
type ConfigMigration struct {
	From int
	Run  func(config map[string]any) error
}

// docMigrations and configMigrations are the upgrades this build knows. Empty for now: every shape stored
// so far is version 1, so there is nothing to upgrade yet. A later change adds its step here, beside the
// version it moves from.
var (
	docMigrations    []DocMigration
	configMigrations = map[string][]ConfigMigration{} // by step type
)

// Load reads a stored workflow document and brings it to this build's shape (see the comment above).
// A document this build can't read, or can't upgrade, comes back with its version as stored, and Validate
// refuses it.
func Load(raw []byte) (Doc, error) {
	return load(raw, SchemaVersion, docMigrations, configMigrations, Definitions())
}

func load(raw []byte, current int, docs []DocMigration, configs map[string][]ConfigMigration, defs []StepDefinition) (Doc, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return Doc{}, err
	}
	if err := upgradeDoc(m, current, docs); err != nil {
		return Doc{}, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Doc{}, err
	}
	var d Doc
	if err := json.Unmarshal(b, &d); err != nil {
		return Doc{}, err
	}
	if storedVersion(m) > current {
		return d, nil // a newer document is read as stored, its nodes included
	}
	typeVersions := map[string]int{}
	for _, def := range defs {
		typeVersions[def.Type] = def.TypeVersion
	}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		want, known := typeVersions[n.Type]
		if !known || n.TypeVersion >= want {
			continue
		}
		upgradeNode(n, want, configs[n.Type])
	}
	return d, nil
}

// upgradeDoc moves m from its stored schemaVersion to current, one step at a time. A missing step leaves m
// as it was (schemaVersion unchanged), which Validate then refuses.
func upgradeDoc(m map[string]any, current int, steps []DocMigration) error {
	v := storedVersion(m)
	if v >= current {
		return nil
	}
	for v < current {
		s, ok := findDocStep(steps, v)
		if !ok {
			return nil
		}
		if err := s.Run(m); err != nil {
			return fmt.Errorf("upgrade workflow from version %d: %w", v, err)
		}
		v++
		m["schemaVersion"] = float64(v)
	}
	return nil
}

// storedVersion is the document's schemaVersion as stored (0 when it has none).
func storedVersion(m map[string]any) int {
	if f, ok := m["schemaVersion"].(float64); ok {
		return int(f)
	}
	return 0
}

// upgradeNode moves n's config from its stored typeVersion to want, one step at a time. A missing step
// leaves n at the version it had, which Validate then refuses.
func upgradeNode(n *Node, want int, steps []ConfigMigration) {
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	for n.TypeVersion < want {
		s, ok := findConfigStep(steps, n.TypeVersion)
		if !ok {
			return
		}
		if err := s.Run(n.Config); err != nil {
			return // a config that can't be upgraded is refused by Validate, as an unknown version is
		}
		n.TypeVersion++
	}
}

func findDocStep(steps []DocMigration, from int) (DocMigration, bool) {
	for _, s := range steps {
		if s.From == from {
			return s, true
		}
	}
	return DocMigration{}, false
}

func findConfigStep(steps []ConfigMigration, from int) (ConfigMigration, bool) {
	for _, s := range steps {
		if s.From == from {
			return s, true
		}
	}
	return ConfigMigration{}, false
}
