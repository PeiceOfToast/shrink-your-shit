// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// The names {{name}} in a step's fields take: the built-ins the engine sets for every file (design-
// joblog-vars section 2), then a workflow's own variables. This file is the server's side of that
// list: the names, their groups and words, and the checks a workflow's own variables must pass.

// NamePattern is what a variable name may be: lowercase letters, numbers and _, starting with a
// letter, at most 40 characters — the editor's rule (VariablesCard), so a name the page takes is one
// the server takes too.
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Builtin is one name the engine sets itself.
type Builtin struct {
	Name        string
	Group       string // File, Library, Video, Audio, Subtitles, Run
	Description string
	// FieldOnly: the name exists in one field only (the .srt name), so it is not offered as a
	// variable of its own until that field takes variables. Fields are the settings it belongs to.
	FieldOnly bool
	Fields    []string
}

// Builtins are every built-in name, in the design's groups and order.
var Builtins = []Builtin{
	{Name: "title", Group: "File", Description: "The file name without its extension"},
	{Name: "file_name", Group: "File", Description: "The file name with its extension"},
	{Name: "ext", Group: "File", Description: "The file's extension"},
	{Name: "folder", Group: "File", Description: "The folder the file is in"},
	{Name: "path", Group: "File", Description: "The full path of the file"},
	{Name: "size_bytes", Group: "File", Description: "The file's size in bytes"},
	{Name: "library", Group: "Library", Description: "The name of the library"},
	{Name: "library_root", Group: "Library", Description: "The library's folder"},
	{Name: "video_codec", Group: "Video", Description: "The video's codec"},
	{Name: "width", Group: "Video", Description: "The width in pixels"},
	{Name: "height", Group: "Video", Description: "The height in pixels"},
	{Name: "resolution", Group: "Video", Description: "The resolution, such as 1080p"},
	{Name: "hdr", Group: "Video", Description: "Whether the video is HDR"},
	{Name: "duration", Group: "Video", Description: "The running time"},
	{Name: "bitrate_kbps", Group: "Video", Description: "The video's bitrate in kbps"},
	{Name: "container", Group: "Video", Description: "The file type the file is in now"},
	{Name: "audio_languages", Group: "Audio", Description: "The audio languages"},
	{Name: "audio_codecs", Group: "Audio", Description: "The audio codecs"},
	{Name: "audio_channels", Group: "Audio", Description: "The channel counts"},
	{Name: "subtitle_languages", Group: "Subtitles", Description: "The subtitle languages"},
	{Name: "workflow", Group: "Run", Description: "The workflow's name"},
	{Name: "workflow_revision", Group: "Run", Description: "The workflow's revision number"},
	{Name: "worker", Group: "Run", Description: "The worker that ran the job"},
	{Name: "encoder", Group: "Run", Description: "The encoder used"},
	{Name: "target_codec", Group: "Run", Description: "The codec the new file uses"},
	{Name: "output_path", Group: "Run", Description: "Where the new file was saved"},
	{Name: "new_size_bytes", Group: "Run", Description: "The new file's size in bytes"},
	{Name: "saved_percent", Group: "Run", Description: "How much smaller the new file is"},
	{Name: "sub_lang", Group: "Subtitles", Description: "The subtitle's language, in the .srt name", FieldOnly: true, Fields: []string{"srtName"}},
	{Name: "sub_kind", Group: "Subtitles", Description: "The subtitle's kind (.forced or .sdh), in the .srt name", FieldOnly: true, Fields: []string{"srtName"}},
}

// BuiltinName reports whether name is a built-in (a name the engine sets, so a workflow can't).
func BuiltinName(name string) bool {
	for _, b := range Builtins {
		if b.Name == name {
			return true
		}
	}
	return false
}

// CheckVariables checks a workflow's own variables: each name is one a person may type, no name is a
// built-in, and no two are the same. The editor refuses these too; the server refuses them as well,
// so a draft saved another way can't break the engine's names.
func CheckVariables(d Doc) []Issue {
	var out []Issue
	seen := map[string]bool{}
	for _, v := range d.Variables {
		switch {
		case !NamePattern.MatchString(v.Name):
			out = append(out, Issue{Code: "invalid_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("%s can't be a variable name: use lowercase letters, numbers and _, starting with a letter, up to 40 characters.", nameOrEmpty(v.Name))})
		case BuiltinName(v.Name):
			out = append(out, Issue{Code: "builtin_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("{{%s}} is set by the server already, so a variable can't use that name.", v.Name)})
		case seen[v.Name]:
			out = append(out, Issue{Code: "duplicate_variable", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("Two variables are called {{%s}}. Give one of them another name.", v.Name)})
		case !secretValueOK(v):
			// a secret is text: only text can be sealed, and a value of another shape would be stored in the
			// clear (W4b QA step 3). null is fine (no value); so is a stored value already sealed.
			out = append(out, Issue{Code: "invalid_secret_value", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("The secret %s has to be text. Send it as a string, or leave it without a value.", nameOrEmpty(v.Name))})
		}
		seen[v.Name] = true
	}
	return out
}

// secretValueOK says whether v's value is one a secret can hold: none (null), the text a person typed, or
// the sealed form the store keeps (a JSON object of the ciphertext and its nonce, which every draft read
// back from the database carries).
func secretValueOK(v Variable) bool {
	if v.Type != "secret" || v.Value == nil {
		return true
	}
	if _, ok := v.Value.(string); ok {
		return true
	}
	m, ok := v.Value.(map[string]any)
	if !ok || len(m) != 2 {
		return false
	}
	sealed, sok := m["sealed"].(string)
	nonce, nok := m["nonce"].(string)
	return sok && nok && sealed != "" && nonce != ""
}

// Masked is d with every secret's value left out: the API never sends one back. HasValue says whether
// one is stored, so the editor can show that it is set without holding it.
func Masked(d Doc) Doc {
	if len(d.Variables) == 0 {
		return d
	}
	out := d
	out.Variables = make([]Variable, len(d.Variables))
	copy(out.Variables, d.Variables)
	for i := range out.Variables {
		v := &out.Variables[i]
		if v.Type != "secret" {
			continue
		}
		v.HasValue = v.HasValue || v.Value != nil
		v.Value = nil
	}
	return out
}

// KeepSecretValues is in with every secret that was sent with no value keeping the one stored has for
// it: the page is never told a secret, so saving the draft back must not erase what it could not show.
// A value that is sent replaces the stored one, and a secret with neither stays without.
func KeepSecretValues(in, stored Doc) Doc {
	have := map[string]any{}
	for _, v := range stored.Variables {
		if v.Type == "secret" && v.Value != nil {
			have[v.Name] = v.Value
		}
	}
	if len(have) == 0 || len(in.Variables) == 0 {
		return in
	}
	out := in
	out.Variables = make([]Variable, len(in.Variables))
	copy(out.Variables, in.Variables)
	for i := range out.Variables {
		v := &out.Variables[i]
		if v.Type != "secret" || v.Value != nil {
			continue
		}
		if val, ok := have[v.Name]; ok {
			v.Value = val
		}
	}
	return out
}

// builtinFor says whether name is a built-in that exists in that setting (a field-only name exists in
// its own settings only).
func builtinFor(name, field string) (Builtin, bool) {
	for _, b := range Builtins {
		if b.Name == name {
			if len(b.Fields) > 0 && !slices.Contains(b.Fields, field) {
				return b, false // a field-only name, in another field
			}
			return b, true
		}
	}
	return Builtin{}, false
}

// PublishedNames are the {{name}} variables the steps of d set for the steps after them (FB1b): a
// "Set a variable" step's name, and every card's declared Publishes.
func PublishedNames(d Doc) []string {
	var out []string
	for _, n := range d.Nodes {
		if n.Type == TypeSetVariable {
			if name, _ := n.Config["name"].(string); name != "" {
				out = append(out, name)
			}
		}
		if def, ok := Definition(n.Type); ok {
			for _, p := range def.Publishes {
				out = append(out, p.Name)
			}
		}
	}
	return out
}

// CheckPublished checks the names steps set and read (FB1b): a "Set a variable" name follows the
// pattern and is not one the server sets; a "Check a variable" reads a name this workflow has.
func CheckPublished(d Doc) []Issue {
	var out []Issue
	known := append(PublishedNames(d), builtinNames()...)
	for _, v := range d.Variables {
		known = append(known, v.Name)
	}
	for _, n := range d.Nodes {
		switch n.Type {
		case TypeSetVariable:
			name, _ := n.Config["name"].(string)
			switch {
			case !NamePattern.MatchString(name):
				out = append(out, Issue{Code: "invalid_variable_name", Severity: "error", NodeID: n.ID, ConfigPath: "/name", Field: "name", Name: name,
					Message: fmt.Sprintf("In '%s', %s can't be a variable name: use lowercase letters, numbers and _, starting with a letter, up to 40 characters.", nodeName(n), nameOrEmpty(name))})
			case BuiltinName(name):
				out = append(out, Issue{Code: "builtin_variable_name", Severity: "error", NodeID: n.ID, ConfigPath: "/name", Field: "name", Name: name,
					Message: fmt.Sprintf("In '%s', {{%s}} is set by the server already, so a step can't set it.", nodeName(n), name)})
			}
		case TypeCheckVariable:
			name, _ := n.Config["variable"].(string)
			if name != "" && !slices.Contains(known, name) {
				i := Issue{Code: "unknown_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/variable", Field: "variable", Name: name,
					Message: fmt.Sprintf("In '%s', {{%s}} is not a variable of this workflow, so the question would always answer No. ", nodeName(n), name)}
				if s := closestName(name, known); s != "" {
					i.Suggest = s
					i.Message += fmt.Sprintf("Did you mean {{%s}}?", s)
				} else {
					i.Message += "Set it with a step before, add it to the workflow, or fix the name."
				}
				out = append(out, i)
			}
		}
	}
	return out
}

func builtinNames() []string {
	out := make([]string, 0, len(Builtins))
	for _, b := range Builtins {
		out = append(out, b.Name)
	}
	return out
}

// CheckTemplates checks every field that takes {{name}} variables ("x-variables": true in its step's
// definition): each name must be one this workflow has, and every {{ must be closed. Publish refuses
// these, so a run never gets a template that would quietly come out wrong: an unknown name becomes
// nothing, and an unclosed {{ makes the rest of the text plain, as typed. A secret is refused too: it
// fills text at run time, but a secret never goes in a file name, folder or title, where it would be
// read by whoever sees the name.
func CheckTemplates(d Doc) []Issue { return CheckTemplatesWith(d, Scopes{}) }

// CheckTemplatesWith is CheckTemplates with the other scopes (W2): a server-wide variable is a name a
// template may use too (the workflow's own of the same name wins at run time, as the lookup order says).
func CheckTemplatesWith(d Doc, sc Scopes) []Issue {
	own := make([]string, 0, len(d.Variables))
	secrets := map[string]bool{}
	for _, v := range d.Variables {
		if v.Type == "secret" {
			secrets[v.Name] = true // never a name a template may use, so not one to suggest either
			continue
		}
		own = append(own, v.Name)
	}
	own = append(own, PublishedNames(d)...) // FB1b: what a step before may have set
	for name, g := range sc.Global {        // W2 (bug from ui-shop's QA of #586): a global counts as known
		if g.Type != "secret" && !slices.Contains(own, name) {
			own = append(own, name)
		}
	}
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	var out []Issue
	for _, n := range d.Nodes {
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
			if p["x-variables"] != true {
				continue
			}
			t, _ := n.Config[k].(string)
			out = append(out, checkTemplate(n, k, titleOf(p, k), t, own, secrets)...)
		}
	}
	return out
}

// secretTemplateIssue is the refusal of one secret named in a template field (the message publish
// finishes with "Nothing was changed.", as it does for every template problem).
func secretTemplateIssue(n Node, field, name string) Issue {
	return Issue{Code: "unknown_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + field, Field: field, Name: name,
		Message: fmt.Sprintf("{{%s}} is a secret, and a secret can't go in a file name, folder or title.", name)}
}

// checkTemplate is one template field checked: its secret names, its unknown names (with a suggested
// one, when a known name is close) and an unclosed {{.
func checkTemplate(n Node, field, title, t string, own []string, secrets map[string]bool) []Issue {
	if t == "" {
		return nil
	}
	refs, unclosed := vars.Refs(t)
	known := append([]string{}, own...)
	for _, b := range Builtins {
		if len(b.Fields) == 0 || slices.Contains(b.Fields, field) {
			known = append(known, b.Name)
		}
	}
	slices.Sort(known)
	var out []Issue
	seen := map[string]bool{}
	for _, r := range refs {
		if secrets[r.Name] {
			if !seen[r.Name] {
				seen[r.Name] = true
				out = append(out, secretTemplateIssue(n, field, r.Name))
			}
			continue
		}
		if _, ok := builtinFor(r.Name, field); ok || slices.Contains(own, r.Name) || seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		if _, reserved := ReservedNames[r.Name]; reserved { // W2 (#596 review): the file's own values fill a whole setting, never text; at run time the name would come out as nothing
			out = append(out, Issue{Code: "unknown_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + field, Field: field, Name: r.Name,
				Message: fmt.Sprintf("In “%s”, {{%s}} is one of the file's own values, which can fill a whole setting but not text, so here it would come out as nothing. Remove it, or give the whole setting to it.", title, r.Name)})
			continue
		}
		i := Issue{Code: "unknown_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + field, Field: field, Name: r.Name,
			Message: fmt.Sprintf("In “%s”, {{%s}} is not a variable of this workflow, so it would come out as nothing. ", title, r.Name)}
		if s := closestName(r.Name, known); s != "" {
			i.Suggest = s
			i.Message += fmt.Sprintf("Did you mean {{%s}}?", s)
		} else {
			i.Message += "Add that variable to the workflow, or fix the name."
		}
		out = append(out, i)
	}
	if unclosed {
		out = append(out, Issue{Code: "unclosed_variable", Severity: "error", NodeID: n.ID, ConfigPath: "/" + field, Field: field,
			Message: fmt.Sprintf("In “%s”, a {{ has no }} after it, so the rest of it would come out word for word. Add the }}, or remove the {{.", title)})
	}
	return out
}

// closestName is the known name nearest to name (at most two letters apart), or "" when none is: a
// typo's likely meaning, never a guess far off.
func closestName(name string, known []string) string {
	best, bestD := "", 3
	for _, k := range known {
		if d := editDistance(name, k); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// nameOrEmpty is a name for a message: the name in quotes, or "A variable" when it is empty.
func nameOrEmpty(name string) string {
	if strings.TrimSpace(name) == "" {
		return "A variable's empty name"
	}
	return fmt.Sprintf("“%s”", name)
}

// configRefs are the names every node's settings refer to ({{name}}), built-ins and the workflow's own included.
func configRefs(d Doc) map[string]bool {
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			refs, _ := vars.Refs(x)
			for _, r := range refs {
				seen[r.Name] = true
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, n := range d.Nodes {
		walk(n.Config)
	}
	return seen
}

// UnsetSecrets are the secrets without a value that d's settings refer to. A run would fill them with nothing,
// so the job names them and stops instead.
func UnsetSecrets(d Doc) []string {
	refs := configRefs(d)
	var out []string
	for _, v := range d.Variables {
		if v.Type == "secret" && (v.Value == nil || v.Value == "") && refs[v.Name] && !slices.Contains(out, v.Name) {
			out = append(out, v.Name)
		}
	}
	slices.Sort(out)
	return out
}
