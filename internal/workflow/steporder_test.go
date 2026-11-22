// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// reordered is the Shrink videos preset with its steps chained in the given types' order (W11).
func reordered(t *testing.T, types ...string) Doc {
	t.Helper()
	d := Preset()
	byType := map[string]Node{}
	for _, n := range d.Nodes {
		byType[n.Type] = n
	}
	if len(types) != len(d.Nodes) {
		t.Fatalf("order has %d steps, the preset has %d", len(types), len(d.Nodes))
	}
	d.Edges = nil
	for i := 0; i+1 < len(types); i++ {
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: byType[types[i]].ID, FromPort: "success", To: byType[types[i+1]].ID, ToPort: "in"})
	}
	return d
}

var (
	chainDefault = []string{TypeInput, TypeSkips, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer, TypeVerify, TypeCommit}
	// the audio, subtitles and file type steps run in the same ffmpeg process as the convert: any order among
	// them and against the convert is honoured
	chainAudioFirst = []string{TypeInput, TypeSkips, TypeAudio, TypeSubtitles, TypeContainer, TypeConvert, TypeVerify, TypeCommit}
	chainFileFirst  = []string{TypeInput, TypeSkips, TypeContainer, TypeAudio, TypeConvert, TypeSubtitles, TypeVerify, TypeCommit}
)

// A reordered path the engine can run compiles to the same settings and the same fingerprint; only the job
// log's step sections change order, and the template records that order.
func TestReorderedStepsCompileInTheirOrder(t *testing.T) {
	base, issues := ToTemplate(reordered(t, chainDefault...), rules.Template{})
	if len(issues) > 0 || base.StepOrder != "" {
		t.Fatalf("default order: issues %+v, stepOrder %q", issues, base.StepOrder)
	}
	for _, c := range []struct {
		name  string
		order []string
		want  string
	}{
		{"audio before the convert", chainAudioFirst, "Audio,Subtitles,File type,Convert video"},
		{"file type before the audio", chainFileFirst, "File type,Audio,Convert video,Subtitles"},
	} {
		tm, issues := ToTemplate(reordered(t, c.order...), rules.Template{})
		if len(issues) > 0 {
			t.Errorf("%s: issues %+v, want none", c.name, issues)
			continue
		}
		if tm.StepOrder != c.want {
			t.Errorf("%s: stepOrder %q, want %q", c.name, tm.StepOrder, c.want)
		}
		v := video(1920, 1080)
		p, want := tm.Compile(v), base.Compile(v)
		if got, wantFP := (rules.Snapshot{Plan: p}).Fingerprint(), (rules.Snapshot{Plan: want}).Fingerprint(); got != wantFP {
			t.Errorf("%s: fingerprint %s, the default order's %s: the reorder changed the result", c.name, got, wantFP)
		}
		if len(p.Steps) != 4 || !reflect.DeepEqual(labels(p), strings.Split(c.want, ",")) {
			t.Errorf("%s: step sections %v, want the workflow's order %v (four sections)", c.name, labels(p), strings.Split(c.want, ","))
		}
	}
}

// The steps the engine can't honour come back as one issue that names the step out of place. (An input that
// is not first never reaches this check: straightChain fails and the existing branch path answers, unchanged by W11.)
func TestUnhonourableStepOrderNamesTheStep(t *testing.T) {
	for _, c := range []struct {
		name    string
		order   []string
		subject string // the step out of place: the issue's NodeID
	}{
		{"convert before skips", []string{TypeInput, TypeConvert, TypeSkips, TypeAudio, TypeSubtitles, TypeContainer, TypeVerify, TypeCommit}, TypeConvert},
		{"verify before the convert", []string{TypeInput, TypeSkips, TypeVerify, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer, TypeCommit}, TypeVerify},
		{"audio after the verify", []string{TypeInput, TypeSkips, TypeConvert, TypeVerify, TypeAudio, TypeSubtitles, TypeContainer, TypeCommit}, TypeVerify},
		{"commit before the verify", []string{TypeInput, TypeSkips, TypeConvert, TypeCommit, TypeAudio, TypeSubtitles, TypeContainer, TypeVerify}, TypeCommit},
	} {
		d := reordered(t, c.order...)
		_, issues := ToTemplate(d, rules.Template{})
		if len(issues) != 1 {
			t.Errorf("%s: issues %+v, want exactly one", c.name, issues)
			continue
		}
		iss := issues[0]
		if iss.Code != "not_runnable_yet" || iss.NodeID != nodeIDOf(d, c.subject) || !strings.Contains(iss.Message, "must come after") {
			t.Errorf("%s: issue %+v, want not_runnable_yet on the %s step with the words 'must come after'", c.name, iss, c.subject)
		}
	}
}

// The chain's own order stores no step order: a flow saved before W11 keeps its bytes.
func TestDefaultOrderStoresNoStepOrder(t *testing.T) {
	tm, issues := ToTemplate(Preset(), rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("issues %+v", issues)
	}
	raw, _ := json.Marshal(tm)
	if strings.Contains(string(raw), "stepOrder") {
		t.Errorf("the default order's template carries a step order: %s", raw)
	}
}

func nodeIDOf(d Doc, typ string) string {
	for _, n := range d.Nodes {
		if n.Type == typ {
			return n.ID
		}
	}
	return ""
}

func labels(p rules.Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Label)
	}
	return out
}
