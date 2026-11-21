// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// W1 (workflow-architecture.md 2.2 rule 3, 2.3): a setting that takes a number or a choice may hold
// one whole variable, {{name}}; its value is checked against the setting like a literal.

// docWith is the preset with variables, and with set applied to it.
func docWith(variables []Variable, set func(*Doc)) Doc {
	d := Preset()
	d.Variables = variables
	if set != nil {
		set(&d)
	}
	return d
}

// configOf gives the step of type typ's settings.
func configOf(t *testing.T, d *Doc, typ string) map[string]any {
	t.Helper()
	for i := range d.Nodes {
		if d.Nodes[i].Type == typ {
			return d.Nodes[i].Config
		}
	}
	t.Fatalf("the preset has no %s step", typ)
	return nil
}

// qualityDoc sets the Convert step's Quality to v.
func qualityDoc(variables []Variable, v any) Doc {
	return docWith(variables, func(d *Doc) {
		for i := range d.Nodes {
			if d.Nodes[i].Type == TypeConvert {
				d.Nodes[i].Config["quality"] = v
			}
		}
	})
}

// A literal, a whole reference in a number or choice setting, spaces around one, a boolean, and an
// integer: each resolves to the variable's own typed value, and the step's schema takes the result.
func TestResolveAWholeFieldValue(t *testing.T) {
	for _, c := range []struct {
		name   string
		vars   []Variable
		typ    string
		field  string
		raw    any
		want   any
		yesRef bool
	}{
		{"a literal stays", nil, TypeConvert, "quality", float64(2), float64(2), false},
		{"a number fills a choice", []Variable{{Name: "q", Type: "number", Value: float64(1)}}, TypeConvert, "quality", "{{q}}", float64(1), true},
		{"spaces around it", []Variable{{Name: "q", Type: "number", Value: float64(4)}}, TypeConvert, "quality", " {{q}} ", float64(4), true},
		{"a number fills a number", []Variable{{Name: "max", Type: "number", Value: float64(4000)}}, TypeConvert, "vbrMaxKbps", "{{max}}", float64(4000), true},
		{"a boolean fills a yes or no", []Variable{{Name: "mine", Type: "boolean", Value: false}}, TypeAudio, "onlyMyLanguages", "{{mine}}", false, true},
		{"text fills a choice the choices have", []Variable{{Name: "kind", Type: "string", Value: "mp4"}}, TypeContainer, "fileType", "{{kind}}", "mp4", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := docWith(c.vars, func(d *Doc) { configOf(t, d, c.typ)[c.field] = c.raw })
			got, issues := Resolve(d)
			if len(issues) != 0 {
				t.Fatalf("issues %+v, want none", issues)
			}
			if c.yesRef != HasRefs(d) {
				t.Errorf("HasRefs %v, want %v", HasRefs(d), c.yesRef)
			}
			if v := configOf(t, &got, c.typ)[c.field]; !reflect.DeepEqual(v, c.want) {
				t.Errorf("%s = %#v, want %#v", c.field, v, c.want)
			}
			// the saved doc keeps what the person typed, so the editor shows the reference
			if v := configOf(t, &d, c.typ)[c.field]; c.yesRef && !reflect.DeepEqual(v, c.raw) {
				t.Errorf("Resolve changed the doc it was given: %s = %#v", c.field, v)
			}
			if r := Validate(d); r.Errors() {
				t.Errorf("Validate refused it: %+v", r.Issues)
			}
		})
	}
}

// What can't fill the setting is refused with the field, the name and a code the page can show: a name
// the workflow doesn't have, a value of the wrong kind, text around the reference, a secret, a
// variable with no value, and a value outside the setting's range.
func TestResolveRefusesWhatDoesNotFit(t *testing.T) {
	for _, c := range []struct {
		name  string
		vars  []Variable
		typ   string
		field string
		raw   any
		code  string
		names string // a name the message must hold
	}{
		{"a name the workflow doesn't have", []Variable{{Name: "quality", Type: "number", Value: float64(2)}},
			TypeConvert, "quality", "{{qualit}}", "unknown_variable", "Did you mean {{quality}}?"},
		{"text into a number", []Variable{{Name: "t", Type: "string", Value: "fast"}},
			TypeConvert, "quality", "{{t}}", "variable_wrong_type", "not a choice"},
		{"text around a reference", []Variable{{Name: "q", Type: "number", Value: float64(2)}},
			TypeConvert, "quality", "{{q}}5", "variable_ref_not_whole", "one whole variable"},
		{"two references", []Variable{{Name: "q", Type: "number", Value: float64(2)}},
			TypeConvert, "quality", "{{q}}{{q}}", "variable_ref_not_whole", "one whole variable"},
		{"an unclosed reference", []Variable{{Name: "q", Type: "number", Value: float64(2)}},
			TypeConvert, "quality", "{{q", "variable_ref_not_whole", "one whole variable"},
		{"a secret", []Variable{{Name: "api_key", Type: "secret", Value: "s3cret"}},
			TypeConvert, "quality", "{{api_key}}", "variable_wrong_type", "is a secret, and a secret can't set this setting"},
		{"a variable with no value", []Variable{{Name: "q", Type: "number"}},
			TypeConvert, "quality", "{{q}}", "variable_wrong_type", "has no value yet"},
		{"below the setting's least", []Variable{{Name: "max", Type: "number", Value: float64(50)}},
			TypeConvert, "vbrMaxKbps", "{{max}}", "variable_wrong_type", "can't be less than 100"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := docWith(c.vars, func(d *Doc) { configOf(t, d, c.typ)[c.field] = c.raw })
			got, issues := Resolve(d)
			if len(issues) != 1 {
				t.Fatalf("issues %+v, want one", issues)
			}
			i := issues[0]
			if i.Code != c.code || i.Severity != "error" || i.Field != c.field || i.ConfigPath != "/"+c.field || i.NodeID == "" {
				t.Errorf("issue %+v, want code %s on %s", i, c.code, c.field)
			}
			if !strings.Contains(i.Message, c.names) {
				t.Errorf("message %q does not say %q", i.Message, c.names)
			}
			// the value stays as it is: nothing is made up to fill it
			if v := configOf(t, &got, c.typ)[c.field]; !reflect.DeepEqual(v, c.raw) {
				t.Errorf("an unresolved value became %#v", v)
			}
			// Validate says it once: the setting is not checked again as a literal
			n := 0
			for _, v := range Validate(d).Issues {
				if v.Field == c.field {
					n++
				}
			}
			if n != 1 {
				t.Errorf("Validate reported the setting %d times: %+v", n, Validate(d).Issues)
			}
			// and the compile refuses it rather than encode at the default
			if _, is := ToTemplate(d, rules.Template{}); len(is) == 0 {
				t.Error("ToTemplate compiled a workflow with a reference it can't resolve")
			}
		})
	}
}

// A workflow with no references is untouched: same doc, same logic hash, and the same plan fingerprint
// for every file as the build before W1 (the fingerprints were taken from a699472c, with the preset and
// a variable nothing uses).
func TestAWorkflowWithoutRefsCompilesAsBefore(t *testing.T) {
	d := docWith([]Variable{{Name: "q", Type: "number", Value: float64(3)}}, nil)
	if HasRefs(d) {
		t.Fatal("HasRefs on a workflow with no references")
	}
	got, issues := Resolve(d)
	if len(issues) != 0 {
		t.Fatalf("issues %+v", issues)
	}
	if !reflect.DeepEqual(got, d) || LogicHash(got) != LogicHash(d) {
		t.Error("Resolve changed a workflow with no references")
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%+v", issues)
	}
	want := []string{"814cfc30bfed7e91", "cdaae850eb2905f5", "96b1fe1cada38047", "299d9c16a882b422", "814cfc30bfed7e91"}
	for i, info := range files() {
		if f := (rules.Snapshot{Plan: tm.Compile(info)}).Fingerprint(); f != want[i] {
			t.Errorf("file %d: plan fingerprint %s, want %s (the plan this workflow always had)", i, f, want[i])
		}
	}
}

// The reference is what the job's plan is made of: quality from the variable, through the workflow and
// back out as a plan, as json does it (a saved doc's numbers come back as float64).
func TestARefMakesThePlan(t *testing.T) {
	d := qualityDoc([]Variable{{Name: "q", Type: "number", Value: float64(1)}}, "{{q}}")
	// the doc as the store keeps it: through json
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var stored Doc
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	tm, issues := ToTemplate(stored, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%+v", issues)
	}
	sel, ok := tm.SelectorsView()
	if !ok || sel.Quality != 1 {
		t.Fatalf("quality %d (a selectors template: %v), want 1", sel.Quality, ok)
	}
	want := rules.SliderTemplate(1).Compile(files()[0])
	if got := tm.Compile(files()[0]); got.Video.CRF != want.Video.CRF {
		t.Errorf("CRF %d, want %d (quality 1's)", got.Video.CRF, want.Video.CRF)
	}
	// the workflow the person saved still says {{q}}
	if v := configOf(t, &stored, TypeConvert)["quality"]; v != "{{q}}" {
		t.Errorf("the stored quality became %#v", v)
	}
}

// The step schema says which settings take a whole reference: exactly these, so the page's picker and
// the server's check read one list (a new one is a deliberate change, named in the handoff).
func TestWholeFieldRefFields(t *testing.T) {
	var got []string
	for _, def := range Definitions() {
		props, _ := def.ConfigSchema["properties"].(map[string]any)
		for k, p := range props {
			prop, _ := p.(map[string]any)
			if prop["x-variableRef"] == true {
				got = append(got, def.Type+"/"+k)
			}
			if prop["x-variableRef"] == true && prop["x-variables"] == true {
				t.Errorf("%s/%s takes whole references and text: one or the other", def.Type, k)
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"audio.tracks/audio", "audio.tracks/onlyMyLanguages",
		"container.output/fileType",
		"filter.video/dolbyVision", "filter.video/fps", "filter.video/hdr",
		"filter.video/minBitrateKbps", "filter.video/resolution", "filter.video/size",
		"filter.video_skips/includeDolbyVision",
		"output.commit/folderMode", "output.commit/ifBigger", "output.commit/policy",
		"subtitles.tracks/subtitles",
		"video.convert/format", "video.convert/maxBitrate", "video.convert/quality", "video.convert/rateControl",
		"video.convert/resolution", "video.convert/speed", "video.convert/vbrMaxKbps",
	}
	if !slices.Equal(got, want) {
		t.Errorf("settings that take a whole reference:\n got %v\nwant %v", got, want)
	}
}
