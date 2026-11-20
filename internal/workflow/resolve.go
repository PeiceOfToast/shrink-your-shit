// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// Whole-field references (W1, workflow-architecture.md 2.2 rule 3 and 2.3). A setting that takes a
// number or a choice — "x-variableRef" in its step's definition — may hold one variable on its own,
// {{name}}, instead of a literal: the Convert step's Quality set to {{q}} encodes at q's value. Text
// around it is refused there (a choice is one value, not a sentence), and the value it resolves to is
// checked against the setting's schema like a literal, so a text variable can't fill a number.
//
// This is the workflow scope only: a workflow's own variables. Global and library scopes come with
// W2/W3, and the file's own names (file.*, source.*) with a later slice; a typed setting names a
// workflow variable or nothing.

// HasRefs reports whether d uses a whole-field reference in a setting that takes a typed value, so a
// store can tell whether a workflow needs resolving at all. A plain workflow (no refs) is left exactly
// as it was, and compiles to the same plan as before.
func HasRefs(d Doc) bool {
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	for _, n := range d.Nodes {
		def, ok := defs[n.Type]
		if !ok {
			continue
		}
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		for k, p := range props {
			prop, _ := p.(map[string]any)
			if prop["x-variableRef"] != true {
				continue
			}
			if s, ok := n.Config[k].(string); ok && strings.Contains(s, "{{") {
				return true
			}
		}
	}
	return false
}

// Resolve is d with every whole-field reference replaced by its variable's value, and what is wrong
// with the ones it can't replace. A doc with no references comes back unchanged (the same slices, so
// it still marshals to the same logic hash). Issues are errors: a reference that can't be resolved
// leaves the setting's own value in place, and Publish refuses the workflow.
func Resolve(d Doc) (Doc, []Issue) {
	own := map[string]Variable{}
	var names []string
	for _, v := range d.Variables {
		own[v.Name] = v
		names = append(names, v.Name)
	}
	slices.Sort(names)
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	var out *Doc
	var issues []Issue
	for i, n := range d.Nodes {
		def, ok := defs[n.Type]
		if !ok {
			continue // unknown_node_type says it already
		}
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		slices.Sort(keys) // one order, whichever order the map answers in
		for _, k := range keys {
			p, _ := props[k].(map[string]any)
			if p["x-variableRef"] != true {
				continue
			}
			s, ok := n.Config[k].(string)
			if !ok || !strings.Contains(s, "{{") {
				continue // a literal, or already a typed value
			}
			title := titleOf(p, k)
			bad := func(code, msg string) {
				issues = append(issues, Issue{Code: code, Severity: "error", NodeID: n.ID, ConfigPath: "/" + k,
					Field: k, Name: wholeRefName(s), Message: "In “" + title + "”, " + msg})
			}
			name, whole := wholeRef(s)
			if !whole {
				bad("variable_ref_not_whole", "put one whole variable here, like {{quality}}, not text around it: "+
					"a number or a choice is one value. Put the reference on its own, or move the text to a field that takes text.")
				continue
			}
			v, known := own[name]
			switch {
			case !known:
				msg := "{{" + name + "}} is not a variable of this workflow. Add that variable, or fix the name."
				i := Issue{Code: "missing_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + k, Field: k, Name: name,
					Message: "In “" + title + "”, " + msg}
				if s := closestName(name, names); s != "" {
					i.Suggest = s
					i.Message = "In “" + title + "”, {{" + name + "}} is not a variable of this workflow. Did you mean {{" + s + "}}?"
				}
				issues = append(issues, i)
			case v.Type == "secret":
				bad("variable_wrong_type", "{{"+name+"}} is a secret, and a secret only fills a field that takes text: "+
					"it can't be written here.")
			case v.Value == nil:
				bad("variable_wrong_type", "{{"+name+"}} has no value yet, so this setting would have nothing. Give it a value, or remove the variable.")
			default:
				if msg := valueProblem(p, v.Value, title); msg != "" {
					bad("variable_wrong_type", "{{"+name+"}} doesn't fit: "+msg)
					continue
				}
				if out == nil {
					cp := d
					cp.Nodes = append([]Node(nil), d.Nodes...)
					out = &cp
				}
				// a copy of this step's settings, the ones resolved before this kept
				cfg := make(map[string]any, len(n.Config))
				for ck, cv := range out.Nodes[i].Config {
					cfg[ck] = cv
				}
				cfg[k] = v.Value
				out.Nodes[i].Config = cfg
			}
		}
	}
	if out == nil {
		return d, issues
	}
	return *out, issues
}

// wholeRef reads a typed setting's value as one whole reference: its name, and whether the value is
// exactly {{name}} (spaces around it allowed). A name with text before or after it, two references,
// or an unclosed {{ is not whole.
func wholeRef(v string) (string, bool) {
	t := strings.TrimSpace(v)
	refs, unclosed := vars.Refs(t)
	if unclosed || len(refs) != 1 || refs[0].Start != 0 || refs[0].End != len(t) {
		return "", false
	}
	return refs[0].Name, true
}

// wholeRefName is the name a bad reference meant, for an issue ("" when it holds no name at all).
func wholeRefName(v string) string {
	t := strings.TrimSpace(v)
	refs, _ := vars.Refs(t)
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Name
}
