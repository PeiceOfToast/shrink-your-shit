// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

var update = flag.Bool("update", false, "rewrite testdata/forms.json")

// formsOf builds every registered step's card the way the API serves it: tags only, no worker
// information.
func formsOf(t *testing.T, env flow.Env) map[string]flow.Form {
	t.Helper()
	out := make(map[string]flow.Form, len(flow.Definitions()))
	for _, def := range flow.Definitions() {
		form, err := flow.BuildForm(def, env)
		if err != nil {
			t.Fatalf("%s: %v", def.Type, err)
		}
		out[def.Type] = form
	}
	return out
}

// TestFormsMatchGolden is step-interface.md point 5: the schema each step serves matches a golden
// file, so a change to a tag is a reviewable diff. Run with -update after changing a tag on purpose.
func TestFormsMatchGolden(t *testing.T) {
	forms := formsOf(t, flow.Env{})
	got, err := json.MarshalIndent(forms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "forms.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden (%v); run go test ./internal/flow/steps -update", err)
	}
	if string(got) != string(want) {
		t.Errorf("the forms changed; run go test ./internal/flow/steps -update and review the diff\n%s",
			firstDifference(string(want), string(got)))
	}
}

// firstDifference is the first differing line, so a golden failure is readable.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) && i < len(g); i++ {
		if w[i] != g[i] {
			return "line " + itoa(i+1) + "\n want: " + w[i] + "\n  got: " + g[i]
		}
	}
	return "one file is longer"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestFormRefusesWhatThePageCannotDraw is the builder's contract: a settings struct it cannot read
// completely is an error, so a new field cannot ship without its form.
func TestFormRefusesWhatThePageCannotDraw(t *testing.T) {
	type noLabel struct {
		A string `set:"a"`
	}
	type unknownShowIf struct {
		A string `set:"a" label:"A"`
		B string `set:"b" label:"B" showIf:"nowhere=x"`
	}
	type undeclaredGroup struct {
		A string `set:"a" label:"A" group:"advanced"`
	}
	type boolChoices struct {
		A bool `set:"a" label:"A" choices:"yes,no"`
	}
	type duplicateSet struct {
		A string `set:"a" label:"A"`
		B string `set:"a" label:"B"`
	}
	type badMin struct {
		A int `set:"a" label:"A" min:"low"`
	}

	cases := []struct {
		name     string
		settings any
		want     string
	}{
		{"no label", noLabel{}, "no label tag"},
		{"showIf to nothing", unknownShowIf{}, "which is not a setting"},
		{"group not declared", undeclaredGroup{}, "does not declare"},
		{"choices on a check box", boolChoices{}, "is not a choice field"},
		{"one name twice", duplicateSet{}, "two fields set"},
		{"min not a number", badMin{}, "min that is not a number"},
	}
	for _, c := range cases {
		_, err := flow.BuildForm(flow.Definition{Type: "test", Settings: c.settings}, flow.Env{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// TestShowIfNamesRealFields checks every dependency in the real steps against the same schema.
func TestShowIfNamesRealFields(t *testing.T) {
	for typ, form := range formsOf(t, flow.Env{}) {
		for name, p := range form.Schema.Properties {
			if p.Layout.ShowIf == nil {
				continue
			}
			target, ok := form.Schema.Properties[p.Layout.ShowIf.Field]
			if !ok {
				t.Errorf("%s.%s shows itself when %q, which the schema does not have", typ, name, p.Layout.ShowIf.Field)
				continue
			}
			if p.Layout.ShowIf.Op != "equals" && p.Layout.ShowIf.Op != "notEquals" {
				t.Errorf("%s.%s has showIf op %q", typ, name, p.Layout.ShowIf.Op)
			}
			if wanted, ok := p.Layout.ShowIf.Value.(string); ok && len(target.Enum) > 0 {
				if !slices.Contains(target.Enum, any(wanted)) {
					t.Errorf("%s.%s waits for %s=%s, which is not one of its choices", typ, name, p.Layout.ShowIf.Field, wanted)
				}
			}
		}
	}
}

// TestPageSevenFields is page 07's field list, against the page: every field, its widget, group,
// unit, range, default and choices. The golden holds the whole schema; this test says why each
// field is there.
func TestPageSevenFields(t *testing.T) {
	forms := formsOf(t, flow.Env{})
	type want struct {
		step   string
		field  string
		widget string
		group  string
		unit   string
	}
	fields := []want{
		// 1 Video, the simple rows.
		{"video.convert", "format", "segmented", "basic", ""},
		{"video.convert", "quality", "slider", "basic", ""},
		{"video.convert", "useGpu", "checkbox", "basic", ""},
		{"video.convert", "ifGpuFails", "segmented", "basic", ""},
		// 1 Video, Advanced.
		{"video.convert", "size", "segmented", "advanced", ""},
		{"video.convert", "width", "number", "advanced", "px"},
		{"video.convert", "height", "number", "advanced", "px"},
		{"video.convert", "rateControl", "segmented", "advanced", ""},
		{"video.convert", "bitrateLevel", "segmented", "advanced", ""},
		{"video.convert", "avgMbps", "number", "advanced", "Mb/s"},
		{"video.convert", "maxMbps", "number", "advanced", "Mb/s"},
		{"video.convert", "twoPass", "checkbox", "advanced", ""},
		{"video.convert", "preset", "select", "advanced", ""},
		{"video.convert", "fps", "number", "advanced", ""},
		{"video.convert", "bitDepth", "segmented", "advanced", ""},
		{"video.convert", "hdr", "segmented", "advanced", ""},
		{"video.convert", "keepDolbyVision", "checkbox", "advanced", ""},
		// 2 Audio, the simple rows.
		{"audio.tracks", "format", "segmented", "basic", ""},
		{"audio.tracks", "channels", "segmented", "basic", ""},
		{"audio.tracks", "allLanguages", "checkbox", "basic", ""},
		{"audio.tracks", "languages", "languages", "basic", ""},
		// 2 Audio, Advanced.
		{"audio.tracks", "codec", "select", "advanced", ""},
		{"audio.tracks", "bitrateKbps", "number", "advanced", "kb/s"},
		{"audio.tracks", "bitratePer", "segmented", "advanced", ""},
		{"audio.tracks", "sampleRate", "number", "advanced", "Hz"},
		{"audio.tracks", "downmix", "select", "advanced", ""},
		{"audio.tracks", "loudnorm", "checkbox", "advanced", ""},
		{"audio.tracks", "commentary", "segmented", "advanced", ""},
		{"audio.tracks", "defaultTrack", "select", "advanced", ""},
		{"audio.tracks", "keepOriginal", "checkbox", "advanced", ""},
		// 2 Audio, Subtitles.
		{"audio.tracks", "subtitles", "segmented", "subtitles", ""},
		{"audio.tracks", "subtitleLanguages", "languages", "subtitles", ""},
		// 3 Save the result.
		{"file.save", "container", "segmented", "basic", ""},
		{"file.save", "where", "segmented", "basic", ""},
		{"file.save", "folder", "text", "basic", ""},
		{"file.save", "nameSuffix", "text", "basic", ""},
	}
	seen := map[string]bool{}
	for _, f := range fields {
		form, ok := forms[f.step]
		if !ok {
			t.Errorf("%s: no such step", f.step)
			continue
		}
		p, ok := form.Schema.Properties[f.field]
		if !ok {
			t.Errorf("%s.%s: missing from the form", f.step, f.field)
			continue
		}
		seen[f.step+"."+f.field] = true
		if p.Layout.Widget != f.widget || p.Layout.Group != f.group || p.Layout.Unit != f.unit {
			t.Errorf("%s.%s: widget/group/unit = %q/%q/%q, want %q/%q/%q",
				f.step, f.field, p.Layout.Widget, p.Layout.Group, p.Layout.Unit, f.widget, f.group, f.unit)
		}
		if p.Title == "" {
			t.Errorf("%s.%s: no title", f.step, f.field)
		}
	}
	// Nothing the page has a row for is missing, and nothing else is served.
	for typ, form := range forms {
		for name := range form.Schema.Properties {
			if !seen[typ+"."+name] {
				t.Errorf("%s.%s: served but not in page 07's list", typ, name)
			}
		}
	}
}

// TestFormValues pins the parts page 07 states exactly: ranges, units, defaults, the words of a
// choice, and the dependencies between fields.
func TestFormValues(t *testing.T) {
	forms := formsOf(t, flow.Env{})

	quality := forms["video.convert"].Schema.Properties["quality"]
	if quality.Minimum == nil || *quality.Minimum != 1 || quality.Maximum == nil || *quality.Maximum != 5 {
		t.Errorf("quality range = %v..%v, want 1..5", quality.Minimum, quality.Maximum)
	}
	if len(quality.Layout.SliderLabels) != 5 || quality.Layout.SliderLabels[0] != "Smallest file" ||
		quality.Layout.SliderLabels[4] != "Best quality" {
		t.Errorf("quality labels = %v", quality.Layout.SliderLabels)
	}
	if quality.Default != int64(3) {
		t.Errorf("quality default = %v, want 3", quality.Default)
	}

	size := forms["video.convert"].Schema.Properties["size"]
	if len(size.Layout.Choices) == 0 || size.Layout.Choices[0].Value != nil ||
		size.Layout.Choices[0].Label != "Same as the source" {
		t.Errorf("size's first choice = %+v, want Same as the source for null", size.Layout.Choices)
	}
	values := make([]any, 0, len(size.Enum))
	for _, v := range size.Enum {
		values = append(values, v)
	}
	for _, v := range []any{nil, "2160", "1080", "720", "480", "custom"} {
		if !slices.Contains(values, v) {
			t.Errorf("size choices = %v, want %v", values, v)
		}
	}
	if slices.Contains(values, any("1440")) {
		t.Errorf("size offers 1440, which page 07's list does not have")
	}

	where := forms["file.save"].Schema.Properties["folder"]
	if where.Layout.ShowIf == nil || where.Layout.ShowIf.Field != "where" || where.Layout.ShowIf.Value != "folder" {
		t.Errorf("folder showIf = %+v, want where=folder", where.Layout.ShowIf)
	}

	maxMbps := forms["video.convert"].Schema.Properties["maxMbps"]
	if maxMbps.Layout.ShowIf == nil || maxMbps.Layout.ShowIf.Value != "vbr" {
		t.Errorf("maxMbps showIf = %+v, want rateControl=vbr", maxMbps.Layout.ShowIf)
	}

	subtitleLanguages := forms["audio.tracks"].Schema.Properties["subtitleLanguages"]
	if subtitleLanguages.Layout.ShowIf == nil || subtitleLanguages.Layout.ShowIf.Value != "my_languages" {
		t.Errorf("subtitleLanguages showIf = %+v, want subtitles=my_languages", subtitleLanguages.Layout.ShowIf)
	}

	// The container default is page 07 rev 6c's MKV; "keep" is still a choice.
	if got := forms["file.save"].Schema.Properties["container"].Default; got != "mkv" {
		t.Errorf("container default = %v, want mkv", got)
	}
}

// TestFormEnvGreysAChoice is the env's part: a choice field tagged needs:"card" is greyed with the
// processor-only sentence until a worker has a card. No page-07 field needs it yet; the card's own
// greying is the check route's job, and this pins the mechanism for the encoder picker.
func TestFormEnvGreysAChoice(t *testing.T) {
	type cardSettings struct {
		Encoder string `set:"encoder" label:"Encoder" choices:"qsv=Intel,vaapi=VA-API" needs:"card"`
	}
	def := flow.Definition{Type: "test.card", Settings: cardSettings{}}

	form, err := flow.BuildForm(def, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	choices := form.Schema.Properties["encoder"].Layout.Choices
	if len(choices) != 2 || choices[0].Available || !strings.Contains(choices[0].Why, "processor only") {
		t.Errorf("choices with no card = %+v, want both greyed with the processor-only sentence", choices)
	}

	form, err = flow.BuildForm(def, flow.Env{CardFamily: "qsv"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range form.Schema.Properties["encoder"].Layout.Choices {
		if !c.Available || c.Why != "" {
			t.Errorf("choice with a card = %+v, want available", c)
		}
	}
}
