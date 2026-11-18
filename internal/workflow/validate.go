// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Result is a Doc checked: its issues, whether the stack can show it without loss, and each node's
// summary in plain words.
type Result struct {
	Issues        []Issue           `json:"issues"`
	Stackable     bool              `json:"stackable"`
	StepSummaries map[string]string `json:"stepSummaries"`
}

// Errors reports whether any issue blocks Publish.
func (r Result) Errors() bool {
	return slices.ContainsFunc(r.Issues, func(i Issue) bool { return i.Severity == "error" })
}

// Validate checks d (spec 5.6): one trigger, every node reachable, no cycle (no loop nodes yet), known
// node types, ports and config values, edges between existing ports, always-on steps on, and whether
// this version can run it. A node's unwired "error" port means stop: the job fails and the original
// stays, as the engine has always done.
func Validate(d Doc) Result {
	r := Result{Issues: []Issue{}, StepSummaries: map[string]string{}}
	add := func(i Issue) {
		if i.Severity == "" {
			i.Severity = "error"
		}
		r.Issues = append(r.Issues, i)
	}
	if d.SchemaVersion != SchemaVersion {
		add(Issue{Code: "unknown_schema_version", Message: fmt.Sprintf("This workflow's format (version %d) is not one this version reads.", d.SchemaVersion)})
		return r
	}
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	byID := map[string]Node{}
	triggers := 0
	for _, n := range d.Nodes {
		if n.ID == "" || byID[n.ID].ID != "" {
			add(Issue{Code: "duplicate_id", NodeID: n.ID, Message: "Two steps have the same id."})
			continue
		}
		byID[n.ID] = n
		def, ok := defs[n.Type]
		if !ok {
			add(Issue{Code: "unknown_node_type", NodeID: n.ID, Message: fmt.Sprintf("%q is not a step this version has.", n.Type)})
			continue
		}
		if n.TypeVersion != def.TypeVersion {
			add(Issue{Code: "unknown_type_version", NodeID: n.ID, Message: fmt.Sprintf("This %s step was made by another version (%d).", def.Name, n.TypeVersion)})
		}
		if n.Type == TypeInput {
			triggers++
		}
		if def.AlwaysOn && !n.Enabled {
			add(Issue{Code: "invalid_config", NodeID: n.ID, Message: def.Name + " can't be turned off."})
		}
		for _, i := range checkConfig(n, def) {
			add(i)
		}
		r.StepSummaries[n.ID] = summary(n)
	}
	switch {
	case triggers == 0:
		add(Issue{Code: "no_trigger", Message: "A workflow starts with Library files."})
	case triggers > 1:
		add(Issue{Code: "multiple_triggers", Message: "A workflow has one Library files step."})
	}
	out := map[string][]string{}
	for _, e := range d.Edges {
		from, okF := byID[e.From]
		to, okT := byID[e.To]
		if !okF || !okT {
			add(Issue{Code: "dangling_edge", EdgeID: e.ID, Message: "A connection leads to a step that isn't there."})
			continue
		}
		if !hasPort(defs[from.Type].Outputs, e.FromPort) {
			add(Issue{Code: "unknown_port", EdgeID: e.ID, NodeID: from.ID, Port: e.FromPort, Message: fmt.Sprintf("%s has no %q outcome.", defs[from.Type].Name, e.FromPort)})
			continue
		}
		if !hasPort(defs[to.Type].Inputs, e.ToPort) {
			add(Issue{Code: "unknown_port", EdgeID: e.ID, NodeID: to.ID, Port: e.ToPort, Message: fmt.Sprintf("%s can't be connected to.", defs[to.Type].Name)})
			continue
		}
		out[e.From] = append(out[e.From], e.To)
	}
	// reachable from the trigger, and no cycle (loop nodes come later)
	for _, n := range d.Nodes {
		if n.Type != TypeInput {
			continue
		}
		seen := map[string]bool{}
		var walk func(string, map[string]bool) bool
		walk = func(id string, onPath map[string]bool) bool {
			if onPath[id] {
				return false
			}
			if seen[id] {
				return true
			}
			seen[id], onPath[id] = true, true
			for _, to := range out[id] {
				if !walk(to, onPath) {
					return false
				}
			}
			delete(onPath, id)
			return true
		}
		if !walk(n.ID, map[string]bool{}) {
			add(Issue{Code: "cycle_without_loop", Message: "Steps lead back to an earlier step. Loops come in a later update."})
		}
		for _, m := range d.Nodes {
			if !seen[m.ID] {
				add(Issue{Code: "unreachable_node", Severity: "warning", NodeID: m.ID, Message: "Nothing leads to this step, so it never runs."})
			}
		}
	}
	_, r.Stackable = straightChain(d)
	if !r.Errors() {
		_, issues := ToTemplate(d, rules.Template{})
		for _, i := range issues {
			add(i)
		}
	}
	return r
}

// checkConfig checks n's config against its definition's schema, the one the registry serves: types
// (integer, number, string, boolean, array, object), enum values, minimum and maximum, maxLength, an
// array's items, and required keys without a default (ui-shop 12:32Z: a minimum of 0 wasn't enforced).
// Each problem names the step and the setting. A key the schema doesn't know is kept and warned about
// (research: never drop it silently).
func checkConfig(n Node, def StepDefinition) []Issue {
	var out []Issue
	if def.ReadOnly { // kept as it is: nothing to check against a form
		return nil
	}
	step := nodeName(n)
	bad := func(k, msg string) {
		out = append(out, Issue{Code: "invalid_config", Severity: "error", NodeID: n.ID, ConfigPath: "/" + k, Field: k,
			Message: fmt.Sprintf("In '%s', %s", step, msg)})
	}
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	if req, ok := def.ConfigSchema["required"].([]string); ok {
		for _, k := range req {
			p, _ := props[k].(map[string]any)
			if _, has := n.Config[k]; !has && p != nil && p["default"] == nil {
				bad(k, fmt.Sprintf("“%s” needs a value.", titleOf(p, k)))
			}
		}
	}
	keys := make([]string, 0, len(n.Config))
	for k := range n.Config {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := n.Config[k]
		p, ok := props[k].(map[string]any)
		if !ok {
			out = append(out, Issue{Code: "unknown_config_key", Severity: "warning", NodeID: n.ID, ConfigPath: "/" + k, Field: k,
				Message: fmt.Sprintf("In '%s', %q is not a setting of this step; it is kept but not used.", step, k)})
			continue
		}
		if msg := valueProblem(p, v, titleOf(p, k)); msg != "" {
			bad(k, msg)
		}
	}
	return out
}

// valueProblem says what is wrong with v for schema p (title names the setting), "" when nothing is.
func valueProblem(p map[string]any, v any, title string) string {
	if enum, ok := p["enum"].([]any); ok {
		if !slices.ContainsFunc(enum, func(e any) bool { return sameValue(e, v) }) {
			return fmt.Sprintf("%v is not a choice for “%s”.", v, title)
		}
		return ""
	}
	if t, _ := p["type"].(string); t != "" && !typeMatches(t, v) {
		return fmt.Sprintf("“%s” has the wrong kind of value (%s).", title, kindWords(t))
	}
	if x, ok := num(v); ok {
		if min, ok := num(p["minimum"]); ok && x < min {
			return fmt.Sprintf("“%s” can't be less than %v.", title, p["minimum"])
		}
		if max, ok := num(p["maximum"]); ok && x > max {
			return fmt.Sprintf("“%s” can't be more than %v.", title, p["maximum"])
		}
	}
	if str, ok := v.(string); ok {
		if max, ok := num(p["maxLength"]); ok && float64(len([]rune(str))) > max {
			return fmt.Sprintf("“%s” can be at most %v characters.", title, p["maxLength"])
		}
	}
	if items, ok := p["items"].(map[string]any); ok {
		var list []any
		switch a := v.(type) {
		case []any:
			list = a
		case []string:
			for _, e := range a {
				list = append(list, e)
			}
		}
		for _, e := range list {
			if msg := valueProblem(items, e, title); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// kindWords is a schema type in plain words.
func kindWords(t string) string {
	return map[string]string{"integer": "a whole number", "number": "a number", "string": "text", "boolean": "yes or no",
		"array": "a list", "object": "a group of settings"}[t]
}

func sameValue(a, b any) bool {
	if fa, ok := num(a); ok {
		fb, ok := num(b)
		return ok && fa == fb
	}
	return a == b
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func typeMatches(t string, v any) bool {
	switch t {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		x, ok := num(v)
		return ok && x == math.Trunc(x)
	case "number":
		_, ok := num(v)
		return ok
	case "array":
		switch v.(type) {
		case []any, []string:
			return true
		}
		return false
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return true
}

func titleOf(p map[string]any, k string) string {
	if t, ok := p["title"].(string); ok {
		return t
	}
	return k
}

func hasPort(ports []Port, name string) bool {
	return slices.ContainsFunc(ports, func(p Port) bool { return p.Name == name })
}

// summary is a node's line in plain words (design: server-rendered, like the old "Your files become").
func summary(n Node) string {
	c := n.Config
	if !n.Enabled { // ui-shop's words per step (registry words pass)
		switch n.Type {
		case TypeAudio:
			return "Off: the original's tracks are kept"
		case TypeSubtitles:
			return "Off: the original's subtitles are kept"
		case TypeContainer:
			return "Off: keeps the original's type"
		}
		return "Off"
	}
	switch n.Type {
	case TypeInput:
		return "Every video in the libraries that use this workflow"
	case TypeSkips:
		if boolOf(c, "includeDolbyVision", false) {
			return "Skips files already in this format and AV1 or VP9 videos; Dolby Vision files become HDR10"
		}
		return "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos"
	case TypeConvert:
		name := "HEVC"
		if f, ok := rules.VideoFormats[strOf(c, "format", "hevc")]; ok {
			name = f.Name
		}
		quality := ""
		if p, ok := rules.SliderAt(intOf(c, "quality", 3)); ok {
			quality = qualityWords(p.Name)
		}
		size := "same resolution"
		if r := strOf(c, "resolution", "keep"); r != "keep" {
			size = "at most " + r + "p"
		}
		parts := []string{name, quality, size}
		if b := strOf(c, "maxBitrate", "none"); b != "none" {
			parts = append(parts, strings.ToUpper(b[:1])+b[1:]+" bitrate limit")
		}
		return strings.Join(parts, " · ")
	case TypeAudio:
		words := rules.AudioWords[strOf(c, "audio", "stereo_aac")]
		if boolOf(c, "onlyMyLanguages", true) {
			words += ", in your languages"
		}
		return words
	case TypeSubtitles:
		return map[string]string{"keep_all": "Keep all", "my_languages": "Only my languages", "text_only": "Text only", "srt": "As SRT files"}[strOf(c, "subtitles", "keep_all")]
	case TypeContainer:
		return map[string]string{"mkv": "MKV", "mp4": "MP4", "webm": "WebM", "keep": "Keep as is"}[strOf(c, "fileType", "mkv")]
	case TypeVerify:
		return "The new file must play through, look like the original and hold what you chose"
	case TypeCommit:
		switch strOf(c, "policy", "separate_folder") {
		case "replace":
			return "Replace the original (kept for a while, then deleted)"
		case "beside":
			return "Next to the original · both stay"
		}
		if strOf(c, "folderMode", "next_to_library") == "one_folder" {
			return "Save to " + strOf(c, "folder", "a folder") + " · originals untouched"
		}
		return "Save next to each library · originals untouched"
	case TypeLegacy:
		return "Settings from an earlier install, run exactly as before"
	}
	return ""
}

// qualityWords is a slider position in the step summary: "Balanced quality" (design SCREENS V3), but a
// name that already says quality or size stands as it is ("Better quality", not "Better quality
// quality"; "Smaller", which is about the file, not "Smaller quality").
func qualityWords(name string) string {
	n := strings.ToLower(name)
	if strings.Contains(n, "quality") || strings.Contains(n, "small") {
		return name
	}
	return name + " quality"
}
