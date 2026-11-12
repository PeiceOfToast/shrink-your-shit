// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// FromTemplate is the workflow for a flow's template (migration, and a flow changed by the old UI):
// the Shrink videos chain for a Basic or Standard flow, or the read-only earlier-install node for any
// other. Node ids are taken from prev by type, so a node keeps its id from one revision to the next.
func FromTemplate(t rules.Template, prev *Doc) Doc {
	ids := map[string]string{}
	if prev != nil {
		for _, n := range prev.Nodes {
			if _, taken := ids[n.Type]; !taken {
				ids[n.Type] = n.ID
			}
		}
	}
	node := func(typ string, config map[string]any) Node {
		id := ids[typ]
		if id == "" {
			id = NewID()
		}
		return Node{ID: id, Type: typ, TypeVersion: 1, Config: config, Enabled: true}
	}
	var nodes []Node
	if s, ok := t.SelectorsView(); ok {
		s.Audio, s.OnlyMyLanguages, _ = rules.StandardAudio(s.Audio, s.OnlyMyLanguages) // Basic's keep_all/my_languages, older values: the engine's own reading
		nodes = []Node{
			node(TypeInput, map[string]any{"ignore": orEmpty(s.Ignore)}),
			node(TypeSkips, map[string]any{"includeDolbyVision": s.IncludeDolbyVision}),
			node(TypeConvert, map[string]any{"quality": s.Quality, "format": s.Format, "resolution": s.Resolution, "maxBitrate": s.MaxBitrate, "speed": s.Speed}),
			node(TypeAudio, map[string]any{"audio": s.Audio, "onlyMyLanguages": s.OnlyMyLanguages}),
			node(TypeSubtitles, map[string]any{"subtitles": s.Subtitles}),
			node(TypeContainer, map[string]any{"fileType": s.FileType}),
			node(TypeVerify, map[string]any{}),
			node(TypeCommit, commitConfig(s)),
		}
	} else {
		raw, _ := json.Marshal(t)
		var tm map[string]any
		json.Unmarshal(raw, &tm)
		nodes = []Node{node(TypeInput, map[string]any{"ignore": []string{}}), node(TypeLegacy, map[string]any{"template": tm})}
	}
	return Doc{SchemaVersion: SchemaVersion, Nodes: nodes, Edges: chain(nodes, prev), Variables: []Variable{}}
}

// Preset is a new workflow's Doc from the Shrink videos preset (design SCREENS V3): design E's defaults
// with design's output-first Save the result.
func Preset() Doc {
	d := FromTemplate(rules.Template{Kind: rules.KindSelectors, Selectors: ptr(rules.DefaultSelectors(3))}, nil)
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeCommit {
			d.Nodes[i].Config = map[string]any{"policy": "separate_folder", "folderMode": "next_to_library"}
		}
	}
	return d
}

func commitConfig(s rules.Selectors) map[string]any {
	switch s.AfterConverting {
	case "folder":
		if s.FolderMode == rules.NextToLibrary {
			return map[string]any{"policy": "separate_folder", "folderMode": rules.NextToLibrary}
		}
		return map[string]any{"policy": "separate_folder", "folderMode": "one_folder", "folder": s.OutputDir}
	case "beside":
		return map[string]any{"policy": "beside"}
	}
	return map[string]any{"policy": "replace"}
}

// chain joins nodes in order by their success ports (the stack, research workflow-schema.md), reusing
// prev's edge ids for the same pairs.
func chain(nodes []Node, prev *Doc) []Edge {
	old := map[[2]string]string{}
	if prev != nil {
		for _, e := range prev.Edges {
			old[[2]string{e.From, e.To}] = e.ID
		}
	}
	edges := []Edge{}
	for i := 1; i < len(nodes); i++ {
		id := old[[2]string{nodes[i-1].ID, nodes[i].ID}]
		if id == "" {
			id = NewID()
		}
		edges = append(edges, Edge{ID: id, From: nodes[i-1].ID, FromPort: "success", To: nodes[i].ID, ToPort: "in"})
	}
	return edges
}

// ToTemplate is what the engine runs for d (V1: a straight chain, compiled by rules as before). current
// is the flow's template now: when d means the same, it is returned unchanged, so a Basic flow stays a
// Basic flow. A Doc this version can't run gives an issue instead (it can still be saved as a draft).
func ToTemplate(d Doc, current rules.Template) (rules.Template, []Issue) {
	order, ok := straightChain(d)
	if !ok { // V6a: a workflow with checks
		return branchTemplate(d)
	}
	types := make([]string, len(order))
	for i, n := range order {
		types[i] = n.Type
	}
	if slices.Equal(types, []string{TypeInput, TypeLegacy}) {
		raw, _ := json.Marshal(order[1].Config["template"])
		var t rules.Template
		if err := json.Unmarshal(raw, &t); err != nil || t.Kind == "" {
			return rules.Template{}, []Issue{{Code: "invalid_config", Severity: "error", NodeID: order[1].ID, ConfigPath: "/template", Message: "These earlier settings can't be read."}}
		}
		return t, nil
	}
	if !slices.Equal(types, PresetOrder) {
		return rules.Template{}, []Issue{notRunnable("This version can run the Shrink videos steps in their order. Other steps and orders come in a later update; you can save it as a draft.", "")}
	}
	s, issues := selectorsOf(order)
	if len(issues) > 0 {
		return rules.Template{}, issues
	}
	if cur, ok := current.SelectorsView(); ok {
		cur.Notes = nil
		cur.Audio, cur.OnlyMyLanguages, _ = rules.StandardAudio(cur.Audio, cur.OnlyMyLanguages)
		if reflect.DeepEqual(cur, s) {
			return current, nil
		}
	}
	return rules.Template{Kind: rules.KindSelectors, Selectors: &s, CompilerVersion: rules.CompilerVersion}, nil
}

// selectorsOf reads the preset chain's configs (missing keys take their defaults; a turned-off audio,
// subtitles or file type step passes the original's through).
func selectorsOf(order []Node) (rules.Selectors, []Issue) {
	s := rules.DefaultSelectors(3)
	s.Ignore = []string{}
	by := map[string]Node{}
	for _, n := range order {
		by[n.Type] = n
	}
	in := by[TypeInput].Config
	switch v := in["ignore"].(type) {
	case []string: // a Doc made here
		s.Ignore = append(s.Ignore, v...)
	case []any: // a Doc read from JSON
		for _, x := range v {
			if p, ok := x.(string); ok {
				s.Ignore = append(s.Ignore, p)
			}
		}
	}
	s.IncludeDolbyVision = boolOf(by[TypeSkips].Config, "includeDolbyVision", false)
	cv := by[TypeConvert].Config
	s.Quality = intOf(cv, "quality", s.Quality)
	s.Format, s.Resolution = strOf(cv, "format", s.Format), strOf(cv, "resolution", s.Resolution)
	s.MaxBitrate, s.Speed = strOf(cv, "maxBitrate", s.MaxBitrate), strOf(cv, "speed", s.Speed)
	if a := by[TypeAudio]; a.Enabled {
		s.Audio, s.OnlyMyLanguages = strOf(a.Config, "audio", s.Audio), boolOf(a.Config, "onlyMyLanguages", s.OnlyMyLanguages)
	} else {
		s.Audio, s.OnlyMyLanguages = "as_is", false
	}
	if sub := by[TypeSubtitles]; sub.Enabled {
		s.Subtitles = strOf(sub.Config, "subtitles", s.Subtitles)
	} else {
		s.Subtitles = "keep_all"
	}
	if c := by[TypeContainer]; c.Enabled {
		s.FileType = strOf(c.Config, "fileType", s.FileType)
	} else {
		s.FileType = "keep"
	}
	commit := by[TypeCommit]
	switch strOf(commit.Config, "policy", "separate_folder") {
	case "replace":
		s.AfterConverting = "replace"
	case "beside":
		s.AfterConverting = "beside"
	case "separate_folder":
		s.AfterConverting = "folder"
		if strOf(commit.Config, "folderMode", rules.NextToLibrary) == rules.NextToLibrary { // V5a: runs now
			s.FolderMode = rules.NextToLibrary
		} else {
			s.OutputDir = strOf(commit.Config, "folder", "")
		}
	default:
		return s, []Issue{{Code: "invalid_config", Severity: "error", NodeID: commit.ID, ConfigPath: "/policy", Message: "Choose how to save the result."}}
	}
	if err := s.Validate(); err != nil {
		msg := strings.TrimSuffix(err.Error(), " Nothing was changed.")
		return s, []Issue{{Code: "invalid_config", Severity: "error", NodeID: by[nodeForMessage(msg)].ID, Message: msg}}
	}
	return s, nil
}

// nodeForMessage is the step a rules.Selectors.Validate message is about.
func nodeForMessage(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "folder") && !strings.Contains(m, "inside the library"):
		return TypeCommit
	case strings.Contains(m, "pattern") || strings.Contains(m, "inside the library"):
		return TypeInput
	case strings.Contains(m, "mp4") || strings.Contains(m, "webm") || strings.Contains(m, "filetype") || strings.Contains(m, "file type"):
		return TypeContainer
	case strings.Contains(m, "audio"):
		return TypeAudio
	case strings.Contains(m, "subtitle"):
		return TypeSubtitles
	}
	return TypeConvert
}

// straightChain is d's nodes in order when they form one path of success edges from the trigger,
// with no other edges (the stack); ok is false otherwise.
func straightChain(d Doc) ([]Node, bool) {
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	next := map[string]string{}
	for _, e := range d.Edges {
		if e.FromPort != "success" || e.ToPort != "in" || next[e.From] != "" {
			return nil, false
		}
		next[e.From] = e.To
	}
	var start string
	for _, n := range d.Nodes {
		if n.Type == TypeInput {
			if start != "" {
				return nil, false
			}
			start = n.ID
		}
	}
	if start == "" {
		return nil, false
	}
	var order []Node
	seen := map[string]bool{}
	for id := start; id != ""; id = next[id] {
		n, ok := byID[id]
		if !ok || seen[id] {
			return nil, false
		}
		seen[id] = true
		order = append(order, n)
	}
	if len(order) != len(d.Nodes) {
		return nil, false
	}
	return order, true
}

func notRunnable(msg, nodeID string) Issue {
	return Issue{Code: "not_runnable_yet", Severity: "error", Message: msg, NodeID: nodeID}
}

func strOf(m map[string]any, k, def string) string {
	if v, ok := m[k].(string); ok && v != "" {
		return v
	}
	return def
}

func boolOf(m map[string]any, k string, def bool) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return def
}

func intOf(m map[string]any, k string, def int) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func ptr[T any](v T) *T { return &v }
