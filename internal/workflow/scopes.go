// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// W2 (workflow-architecture.md 2.3 and 2.4): the values a whole-field reference can take besides the
// workflow's own variables. Lookup is most specific first: the workflow's own variable, then the
// library's (W3 adds its column; the hook is here), then the server's global variables, then the
// file's own reserved names. A name none of them has is unknown_variable, with a suggestion when a
// close name exists.
//
// Validate and publish resolve with the globals and leave file.* and source.* references in place:
// there is no file yet, so only their kinds can be checked. The job resolves them when it has one.

// Scopes are the values a whole-field reference can resolve from other than d.Variables, and what
// this install allows the doc to do.
type Scopes struct {
	Global  map[string]Variable // the server's global variables (Settings > Workflows)
	Library map[string]Variable // the library's own (W3; nil until it has a column)
	File    map[string]any      // file.* and source.* values for one file, at job start
	// LeaveFile keeps file.* and source.* references in place, their kinds checked against the
	// setting. Validate and publish use it; the job fills the values.
	LeaveFile bool
	// Policy is the install's Safety switch (FB2b). Its zero value allows nothing: the server passes
	// the live policy, and Validate assumes everything is allowed.
	Policy Policy
}

// Reserved is one of the file's own names: what kind of value it has, and what it means.
type Reserved struct {
	Type        string // the JSON schema type of its value
	Description string
}

// ReservedNames are the file's own names (W2). A name with a dot in it can never be a variable of a
// workflow, a library or the server (NamePattern has no dot), so these are the file's alone.
var ReservedNames = map[string]Reserved{
	"file.name":        {Type: "string", Description: "The file's name, without its folder"},
	"file.size":        {Type: "number", Description: "The file's size in bytes"},
	"file.folder":      {Type: "string", Description: "The folder the file is in"},
	"source.codec":     {Type: "string", Description: "The video's codec"},
	"source.height":    {Type: "number", Description: "The video's height in pixels"},
	"source.hdr":       {Type: "boolean", Description: "Whether the video is HDR"},
	"source.bit_depth": {Type: "number", Description: "The video's colour depth in bits"},
}

// reservedNames are ReservedNames' names, sorted (one suggestion pool, whatever order the map answers in).
func reservedNames() []string {
	out := make([]string, 0, len(ReservedNames))
	for n := range ReservedNames {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// VariableMap are variables by name, the shape a scope takes.
func VariableMap(vs []Variable) map[string]Variable {
	out := make(map[string]Variable, len(vs))
	for _, v := range vs {
		out[v.Name] = v
	}
	return out
}

// FileValues are the reserved names for one file, as a job resolves them. What the probe could not say
// is left out, so a setting that names it is refused for this file rather than filled with a zero.
func FileValues(path string, src media.Info, size int64) map[string]any {
	out := map[string]any{"file.name": filepath.Base(path), "file.folder": filepath.Dir(path)}
	switch {
	case src.SizeBytes > 0:
		out["file.size"] = src.SizeBytes
	case size > 0:
		out["file.size"] = size
	}
	if v := src.Video(); v != nil {
		if v.Codec != "" {
			out["source.codec"] = v.Codec
		}
		if v.Height > 0 {
			out["source.height"] = v.Height
		}
		if v.BitDepth > 0 {
			out["source.bit_depth"] = v.BitDepth
		}
		out["source.hdr"] = src.IsHDR()
	}
	return out
}

// Usage is one reference a doc's typed settings used: the value it resolved to, and where the value
// came from ("workflow", "library", "global", "file" or "source"). The job log's variables table
// shows the origin, so a value from Settings reads as 24 (global), not as one the workflow set.
type Usage struct {
	Name   string
	Value  any
	Origin string
}

// lookup is a name through the scopes, most specific first.
func (s Scopes) lookup(name string, own map[string]Variable) (Variable, string, bool) {
	if v, ok := own[name]; ok {
		return v, "workflow", true
	}
	if v, ok := s.Library[name]; ok {
		return v, "library", true
	}
	if v, ok := s.Global[name]; ok {
		return v, "global", true
	}
	return Variable{}, "", false
}

// reservedProblem says whether a reserved name's kind can fill schema p when there is no file yet ("":
// it can). The value itself is checked when the job has the file.
func reservedProblem(r Reserved, p map[string]any, title string) string {
	var zero any
	switch r.Type {
	case "string":
		zero = ""
	case "number":
		zero = float64(0)
	case "boolean":
		zero = false
	}
	if t, _ := p["type"].(string); t != "" && !typeMatches(t, zero) {
		return "is " + kindWords(r.Type) + ", and “" + title + "” takes " + kindWords(t) + "."
	}
	if enum, ok := p["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return typeMatches(r.Type, e) }) {
		return "is " + kindWords(r.Type) + ", which “" + title + "” can't take."
	}
	return ""
}

// RefNames are the variable names d's typed settings hold, each once, sorted: what saving a global
// counts the workflows it affects by.
func RefNames(d Doc) []string {
	seen := map[string]bool{}
	for _, n := range d.Nodes {
		def, ok := Definition(n.Type)
		if !ok {
			continue
		}
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		for k, p := range props {
			prop, _ := p.(map[string]any)
			if prop["x-variableRef"] != true {
				continue
			}
			s, ok := n.Config[k].(string)
			if !ok || !strings.Contains(s, "{{") {
				continue
			}
			if name, whole := wholeRef(s); whole {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// globalTypes are the kinds a global variable may be. A secret is not one: it is sealed per workflow
// and opened only for that workflow's job, so a server-wide secret would be one every workflow reads.
var globalTypes = []string{"string", "number", "bool", "boolean", "enum", "path"}

// CheckGlobals checks the server's global variables: the same name rules a workflow's own have, and a
// value that fits its kind. A global is text, a number, yes or no, a choice, or a path.
func CheckGlobals(vs []Variable) []Issue {
	var out []Issue
	seen := map[string]bool{}
	for _, v := range vs {
		switch {
		case !NamePattern.MatchString(v.Name):
			out = append(out, Issue{Code: "invalid_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("%s can't be a variable name: use lowercase letters, numbers and _, starting with a letter, up to 40 characters.", nameOrEmpty(v.Name))})
		case BuiltinName(v.Name):
			out = append(out, Issue{Code: "builtin_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("{{%s}} is set by the server already, so a variable can't use that name.", v.Name)})
		case seen[v.Name]:
			out = append(out, Issue{Code: "duplicate_variable", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("Two global variables are called {{%s}}. Give one of them another name.", v.Name)})
		case !slices.Contains(globalTypes, v.Type):
			out = append(out, Issue{Code: "invalid_variable_type", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("A global variable is text, a number, yes or no, a choice, or a path; %s is not one. A secret stays in its workflow.", nameOrEmpty(v.Name))})
		default:
			if msg := globalValueProblem(v); msg != "" {
				out = append(out, Issue{Code: "variable_wrong_type", Severity: "error", Name: v.Name,
					Message: fmt.Sprintf("The global variable %s %s. Give it a value of that kind, or leave it without one.", nameOrEmpty(v.Name), msg)})
			}
		}
		seen[v.Name] = true
	}
	return out
}

// globalValueProblem says what is wrong with v's value for its kind ("" when nothing is).
func globalValueProblem(v Variable) string {
	if v.Value == nil {
		return ""
	}
	switch v.Type {
	case "string", "path":
		if _, ok := v.Value.(string); !ok {
			return "has to be text"
		}
	case "number":
		if _, ok := num(v.Value); !ok {
			return "has to be a number"
		}
	case "bool", "boolean":
		if _, ok := v.Value.(bool); !ok {
			return "has to be yes or no"
		}
	case "enum":
		s, ok := v.Value.(string)
		if !ok {
			return "has to be one of its choices, as text"
		}
		if len(v.Options) > 0 && !slices.Contains(v.Options, s) {
			return "has to be one of its choices"
		}
	}
	return ""
}

// keptFileRef reports whether v is one whole reference to a reserved file name: Validate and publish
// keep it in the setting, and the job fills it from the file it has.
func keptFileRef(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	name, whole := wholeRef(s)
	if !whole {
		return false
	}
	_, isFile := ReservedNames[name]
	return isFile
}
