// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"math"
	"slices"
	"strconv"
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

// HasFileRefs reports whether a typed setting of d names one of the file's own values (file.*,
// source.*), which only a job with the file in hand can fill. A workflow handed files by another
// (FB4b) is compiled without one, so such a target is refused (review-579).
func HasFileRefs(d Doc) bool {
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
			if s, ok := n.Config[k].(string); ok { // the resolver's own reader, so whitespace reads the same (review2-579 P3-2)
				if name, whole := wholeRef(s); whole {
					if _, reserved := ReservedNames[name]; reserved {
						return true
					}
				}
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
	out, issues, _ := resolveOnce(d, Scopes{})
	return out, issues
}

// ResolveWith is Resolve with the other scopes (W2): the workflow's own variable wins, then the
// library's, then a server-wide one, then the file's reserved names. It also says which names the
// settings used and where each value came from, for the job log's variables table.
func ResolveWith(d Doc, s Scopes) (Doc, []Issue, []Usage) {
	return resolveOnce(d, s)
}

func resolveOnce(d Doc, sc Scopes) (Doc, []Issue, []Usage) {
	own := VariableMap(d.Variables)
	names := make([]string, 0, len(own)+len(sc.Library)+len(sc.Global)+len(ReservedNames))
	for n := range own {
		names = append(names, n)
	}
	for n := range sc.Library {
		names = append(names, n)
	}
	for n := range sc.Global {
		names = append(names, n)
	}
	names = append(names, reservedNames()...)
	slices.Sort(names) // one suggestion order, whichever order the maps answer in
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	var out *Doc
	var issues []Issue
	var used []Usage
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
			set := func(val any) { // the doc copy, and this one setting in it
				if out == nil {
					cp := d
					cp.Nodes = append([]Node(nil), d.Nodes...)
					out = &cp
				}
				cfg := make(map[string]any, len(n.Config))
				for ck, cv := range out.Nodes[i].Config {
					cfg[ck] = cv
				}
				cfg[k] = val
				out.Nodes[i].Config = cfg
			}
			check := func(name string, val any) (any, bool) {
				// W1 fix: a value typed into a variable is text ("4"), and Quality's choices are the
				// numbers 1-5; a setting that wants a number takes exactly a number written as text.
				if num, ok := numberFor(p, val); ok {
					val = num
				}
				if msg := valueProblem(p, val, title); msg != "" {
					bad("variable_wrong_type", "{{"+name+"}} doesn't fit: "+msg)
					return nil, false
				}
				return val, true
			}
			name, whole := wholeRef(s)
			if !whole {
				bad("variable_ref_not_whole", "put one whole variable here, like {{quality}}, not text around it: "+
					"a number or a choice is one value. Put the reference on its own, or move the text to a field that takes text.")
				continue
			}
			v, origin, known := sc.lookup(name, own)
			if !known {
				// the file's own name: the job fills it; here its kind has to fit
				if r, ok := ReservedNames[name]; ok {
					if sc.LeaveFile {
						if msg := reservedProblem(r, p, title); msg != "" {
							bad("variable_wrong_type", "{{"+name+"}} "+msg)
						}
						continue
					}
					val, has := sc.File[name]
					if !has {
						bad("variable_wrong_type", "{{"+name+"}} has no value for this file.")
						continue
					}
					if val, ok := check(name, val); ok {
						set(val)
						used = append(used, Usage{Name: name, Value: val, Origin: strings.SplitN(name, ".", 2)[0]})
					}
					continue
				}
				i := Issue{Code: "unknown_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + k, Field: k, Name: name,
					Message: "In “" + title + "”, {{" + name + "}} is not a variable of this workflow, its library or the server. Add that variable, or fix the name."}
				if s := closestName(name, names); s != "" {
					i.Suggest = s
					i.Message = "In “" + title + "”, {{" + name + "}} is not a variable of this workflow, its library or the server. Did you mean {{" + s + "}}?"
				}
				issues = append(issues, i)
				continue
			}
			switch {
			case v.Type == "secret":
				bad("variable_wrong_type", "{{"+name+"}} is a secret, and a secret can't set this setting.")
			case v.Value == nil:
				bad("variable_wrong_type", "{{"+name+"}} has no value yet, so this setting would have nothing. Give it a value, or remove the variable.")
			default:
				if val, ok := check(name, v.Value); ok {
					set(val)
					used = append(used, Usage{Name: name, Value: val, Origin: origin})
				}
			}
		}
	}
	if out == nil {
		return d, issues, used
	}
	return *out, issues, used
}

// numberFor is v as a number when the setting wants one and v is text that is exactly a number: the
// value a person typed into a variable's form, which a choice or a number setting here takes (W1 fix,
// found on the live server: Quality set to {{level}}, level "4"). The setting wants a number when its
// schema says integer or number, or its choices are numbers. Anything else keeps v as it is: text for
// a text setting, and "abc" or "4.5" for a whole number — those are still what valueProblem refuses.
func numberFor(p map[string]any, v any) (any, bool) {
	s, ok := v.(string)
	if !ok {
		return v, false
	}
	s = strings.TrimSpace(s)
	whole := false
	switch t, _ := p["type"].(string); t {
	case "integer":
		whole = true
	case "number":
	default:
		// no number type: a choice, when its choices are numbers
		enum, ok := p["enum"].([]any)
		if !ok || !slices.ContainsFunc(enum, func(e any) bool { _, isNum := num(e); return isNum }) {
			return v, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || whole && f != math.Trunc(f) {
		return v, false
	}
	return f, true
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
