// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// W2 (workflow-architecture.md 2.3 and 2.4): the lookup order, the reserved file names, the globals'
// own checks, and what a save can say it affects.

// A typed setting takes the workflow's own variable first, then the library's, then the server's
// global one; each use says where its value came from. W3: a variable the workflow marks templateParam
// is the one a library may change, so the library's value comes before the workflow's own.
func TestTheLookupOrderIsMostSpecificFirst(t *testing.T) {
	own := []Variable{{Name: "q", Type: "number", Value: float64(1)}}
	ownParam := []Variable{{Name: "q", Type: "number", Value: float64(1), TemplateParam: true}}
	lib := []Variable{{Name: "q", Type: "number", Value: float64(3)}, {Name: "l", Type: "number", Value: float64(5)}}
	glob := []Variable{{Name: "q", Type: "number", Value: float64(2)}, {Name: "g", Type: "number", Value: float64(4)}}
	for _, c := range []struct {
		name    string
		own     []Variable
		sc      Scopes
		ref     string
		want    int
		origin  string
		usedOne string
	}{
		{"the workflow's own wins", own, Scopes{Library: VariableMap(lib), Global: VariableMap(glob)}, "{{q}}", 1, "workflow", "q"},
		{"then the library's", nil, Scopes{Library: VariableMap(lib), Global: VariableMap(glob)}, "{{q}}", 3, "library", "q"},
		{"then the server's", nil, Scopes{Global: VariableMap(glob)}, "{{q}}", 2, "global", "q"},
		{"a library name nothing else has", nil, Scopes{Library: VariableMap(lib), Global: VariableMap(glob)}, "{{l}}", 5, "library", "l"},
		{"a global name nothing else has", nil, Scopes{Global: VariableMap(glob)}, "{{g}}", 4, "global", "g"},
		{"a variable the workflow lets a library change takes the library's", ownParam, Scopes{Library: VariableMap(lib), Global: VariableMap(glob)}, "{{q}}", 3, "library", "q"},
		{"a library value that isn't set keeps the workflow's", ownParam, Scopes{Library: VariableMap([]Variable{{Name: "q", Type: "number"}}), Global: VariableMap(glob)}, "{{q}}", 1, "workflow", "q"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, issues, used := ResolveWith(qualityDoc(c.own, c.ref), c.sc)
			if len(issues) != 0 {
				t.Fatalf("%+v", issues)
			}
			if v := configOf(t, &got, TypeConvert)["quality"]; !reflect.DeepEqual(v, float64(c.want)) {
				t.Errorf("quality %#v, want %d", v, c.want)
			}
			if len(used) != 1 || used[0].Name != c.usedOne || used[0].Origin != c.origin || !reflect.DeepEqual(used[0].Value, float64(c.want)) {
				t.Errorf("used %+v, want one %s from %s", used, c.usedOne, c.origin)
			}
		})
	}
}

// W2 ask (review-560 mutation M2): a setting that takes text keeps a "4" variable as text. No step
// registered today takes text as a whole reference, so this pins the rule where it lives: numberFor
// converts only a setting whose schema wants a number or whose choices are numbers.
func TestATextSettingKeepsNumericTextAsText(t *testing.T) {
	for _, p := range []map[string]any{
		{"type": "string"},
		{"type": "string", "maxLength": 200},
		{"type": "string", "enum": []any{"a", "b"}},
	} {
		if v, ok := numberFor(p, "4"); ok || v != "4" {
			t.Errorf("%v: %#v, %v; want the text \"4\" kept", p, v, ok)
		}
	}
}

// A name nobody defines is unknown_variable, and a global that is close is the suggestion: the chain
// is what the person is told about, not just the workflow.
func TestAnUnknownNameSuggestsAGlobal(t *testing.T) {
	d := qualityDoc(nil, "{{qualit}}")
	_, issues, _ := ResolveWith(d, Scopes{Global: VariableMap([]Variable{{Name: "quality", Type: "number", Value: float64(2)}})})
	if len(issues) != 1 || issues[0].Code != "unknown_variable" || issues[0].Suggest != "quality" {
		t.Fatalf("%+v, want one unknown_variable suggesting quality", issues)
	}
	if !strings.Contains(issues[0].Message, "its library or the server") {
		t.Errorf("the message names only the workflow: %q", issues[0].Message)
	}
}

// Validate and publish have no file: file.* and source.* stay references, their kinds checked against
// the setting (a height can't fill a choice of text).
func TestFileNamesStayReferencesUntilThereIsAFile(t *testing.T) {
	sc := Scopes{LeaveFile: true}
	d := qualityDoc(nil, "{{source.height}}") // Quality's choices are the numbers 1-5: a number fits
	if _, issues, _ := ResolveWith(d, sc); len(issues) != 0 {
		t.Errorf("a height for Quality: %+v", issues)
	}
	if r := ValidateWith(d, sc); r.Errors() {
		t.Errorf("Validate refused it: %+v", r.Issues)
	}
	if _, issues := ToTemplateWith(d, rules.Template{}, sc); len(issues) != 0 {
		t.Errorf("ToTemplate refused it: %+v", issues)
	}

	kind := qualityDoc(nil, 3)
	configOf(t, &kind, TypeContainer)["fileType"] = "{{source.height}}"
	_, issues, _ := ResolveWith(kind, sc)
	if len(issues) != 1 || issues[0].Code != "variable_wrong_type" || !strings.Contains(issues[0].Message, "can't take") {
		t.Errorf("%+v, want one variable_wrong_type saying the kind can't fill it", issues)
	}

	// without a file and not leaving it, there is nothing to fill it with
	if _, issues := Resolve(qualityDoc(nil, "{{source.height}}")); len(issues) != 1 || !strings.Contains(issues[0].Message, "has no value for this file") {
		t.Errorf("%+v, want one saying it has no value for this file", issues)
	}
}

// With the file the job has, file.* and source.* fill the settings, and each use says where it came
// from (the file or the source).
func TestTheFilesOwnValuesFillTheSettings(t *testing.T) {
	src := media.Info{SizeBytes: 2048, Streams: []media.Stream{{Type: "video", Codec: "hevc", Width: 1920, Height: 1080, BitDepth: 10}}}
	for _, c := range []struct {
		ref    string
		want   any
		origin string
	}{
		{"{{source.height}}", 1080, "source"},
		{"{{file.size}}", int64(2048), "file"},
	} {
		d := qualityDoc(nil, 3)
		configOf(t, &d, TypeConvert)["vbrMaxKbps"] = c.ref
		configOf(t, &d, TypeConvert)["rateControl"] = "vbr"
		got, issues, used := ResolveWith(d, Scopes{File: FileValues("/videos/Cat.mkv", src, 0)})
		if len(issues) != 0 {
			t.Fatalf("%s: %+v", c.ref, issues)
		}
		if v := configOf(t, &got, TypeConvert)["vbrMaxKbps"]; !reflect.DeepEqual(v, c.want) {
			t.Errorf("%s: vbrMaxKbps %#v, want %d", c.ref, v, c.want)
		}
		if len(used) != 1 || used[0].Origin != c.origin || !reflect.DeepEqual(used[0].Value, c.want) {
			t.Errorf("%s: used %+v, want one from %s", c.ref, used, c.origin)
		}
	}
	// the folder comes back in this OS's own form (P2-2: Windows builds "\\videos"), so the
	// expectation is built the same way rather than as a POSIX literal
	cat := filepath.Join("/videos", "Cat.mkv")
	fv := FileValues(cat, src, 0)
	if fv["file.name"] != "Cat.mkv" || fv["file.folder"] != filepath.Dir(cat) || fv["file.size"] != int64(2048) ||
		fv["source.codec"] != "hevc" || fv["source.height"] != 1080 || fv["source.bit_depth"] != 10 || fv["source.hdr"] != false {
		t.Errorf("FileValues %v", fv)
	}
}

// What the file's probe could not say is left out, so a setting that names it is refused rather than
// filled with a zero.
func TestAFileValueTheProbeDidNotFindIsNotInvented(t *testing.T) {
	d := qualityDoc(nil, 3)
	configOf(t, &d, TypeConvert)["vbrMaxKbps"] = "{{source.bit_depth}}"
	configOf(t, &d, TypeConvert)["rateControl"] = "vbr"
	_, issues, _ := ResolveWith(d, Scopes{File: FileValues("/videos/Cat.mkv", media.Info{}, 0)})
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "has no value for this file") {
		t.Errorf("%+v, want one saying it has no value for this file", issues)
	}
}

// RefNames is what a save counts its affected workflows by: every typed name once, sorted.
func TestRefNamesAreTheTypedNames(t *testing.T) {
	d := qualityDoc(nil, "{{q}}")
	configOf(t, &d, TypeConvert)["vbrMaxKbps"] = "{{source.height}}"
	configOf(t, &d, TypeContainer)["fileType"] = "{{kind}}"
	if got, want := RefNames(d), []string{"kind", "q", "source.height"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RefNames %v, want %v", got, want)
	}
}

// A global follows the same name rules a workflow's own variables do, is one of five kinds (a secret
// stays in its workflow), and its value fits that kind.
func TestCheckGlobals(t *testing.T) {
	for _, c := range []struct {
		name string
		v    Variable
		code string
	}{
		{"a name a person can type", Variable{Name: "q", Type: "number", Value: float64(2)}, ""},
		{"text", Variable{Name: "kind", Type: "string", Value: "mp4"}, ""},
		{"yes or no", Variable{Name: "tone", Type: "bool", Value: true}, ""},
		{"a choice", Variable{Name: "tier", Type: "enum", Options: []string{"a", "b"}, Value: "a"}, ""},
		{"no value yet", Variable{Name: "q", Type: "number"}, ""},
		{"a name with a dot", Variable{Name: "a.b", Type: "string", Value: "x"}, "invalid_variable_name"},
		{"a built-in name", Variable{Name: "title", Type: "string", Value: "x"}, "builtin_variable_name"},
		{"a secret", Variable{Name: "key", Type: "secret", Value: "s"}, "invalid_variable_type"},
		{"a number that is text", Variable{Name: "q", Type: "number", Value: "2"}, "variable_wrong_type"},
		{"a choice that isn't one", Variable{Name: "tier", Type: "enum", Options: []string{"a", "b"}, Value: "c"}, "variable_wrong_type"},
	} {
		got := CheckGlobals([]Variable{c.v})
		if c.code == "" {
			if len(got) != 0 {
				t.Errorf("%s: %+v, want none", c.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Code != c.code {
			t.Errorf("%s: %+v, want one %s", c.name, got, c.code)
		}
	}
	if got := CheckGlobals([]Variable{{Name: "q", Type: "number", Value: float64(1)}, {Name: "q", Type: "number", Value: float64(2)}}); len(got) != 1 || got[0].Code != "duplicate_variable" {
		t.Errorf("two of a name: %+v, want one duplicate_variable", got)
	}
}

// W3 (workflow-architecture.md 2.2 rule 1): a library may set only a variable its workflow marks
// templateParam; anything else is override_not_allowed, and a value has to fit the workflow's kind.
func TestCheckLibraryOverrides(t *testing.T) {
	d := docWith([]Variable{
		{Name: "q", Type: "number", Value: float64(1), TemplateParam: true},
		{Name: "k", Type: "number", Value: float64(1)},
		{Name: "key", Type: "secret", TemplateParam: true},
		{Name: "kind", Type: "string", TemplateParam: true},
		{Name: "tier", Type: "enum", Options: []string{"a", "b"}, TemplateParam: true},
	}, nil)
	for _, c := range []struct {
		name string
		v    Variable
		code string
	}{
		{"a variable the workflow lets a library change", Variable{Name: "q", Value: float64(4)}, ""},
		{"no value yet", Variable{Name: "q"}, ""},
		{"a text variable", Variable{Name: "kind", Value: "mp4"}, ""},
		{"a choice", Variable{Name: "tier", Value: "a"}, ""},
		{"a variable the workflow keeps", Variable{Name: "k", Value: float64(2)}, "override_not_allowed"},
		{"a name the workflow doesn't have", Variable{Name: "nope", Value: float64(2)}, "override_not_allowed"},
		{"a name with a dot", Variable{Name: "a.b", Value: "x"}, "invalid_variable_name"},
		{"a secret", Variable{Name: "key", Value: "s"}, "variable_wrong_type"},
		{"a number that is text", Variable{Name: "q", Value: "4"}, "variable_wrong_type"},
		{"a choice that isn't one", Variable{Name: "tier", Value: "c"}, "variable_wrong_type"},
	} {
		got := CheckLibraryOverrides(d, []Variable{c.v})
		if c.code == "" {
			if len(got) != 0 {
				t.Errorf("%s: %+v, want none", c.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Code != c.code || got[0].Name != c.v.Name {
			t.Errorf("%s: %+v, want one %s naming %q", c.name, got, c.code, c.v.Name)
		}
	}
	got := CheckLibraryOverrides(d, []Variable{{Name: "q", Value: float64(1)}, {Name: "q", Value: float64(2)}})
	if len(got) != 1 || got[0].Code != "duplicate_variable" {
		t.Errorf("two of a name: %+v, want one duplicate_variable", got)
	}
	// the refusal is in words a person can act on, and names the variable they set
	got = CheckLibraryOverrides(d, []Variable{{Name: "k", Value: float64(2)}})
	if len(got) != 1 || !strings.Contains(got[0].Message, "“k”") || !strings.Contains(got[0].Message, "doesn't let a library change it") {
		t.Errorf("the refusal: %+v, want it to name the variable and say why", got)
	}
}
