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

// The steps the engine can't honour come back as one issue that names the step out of place.
func TestUnhonourableStepOrderNamesTheStep(t *testing.T) {
	for _, c := range []struct {
		name    string
		order   []string
		subject string // the step out of place: the issue's NodeID
	}{
		{"input not first", []string{TypeSkips, TypeInput, TypeConvert, TypeAudio, TypeSubtitles, TypeContainer, TypeVerify, TypeCommit}, TypeSkips},
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

// The shortcut that keeps a saved template as it is compares the step order too: a saved audio-first
// template, passed as current with the default chain, comes back in the default order.
func TestSavedReorderDoesNotSurviveTheDefaultChain(t *testing.T) {
	saved, issues := ToTemplate(reordered(t, chainAudioFirst...), rules.Template{})
	if len(issues) > 0 || saved.StepOrder == "" {
		t.Fatalf("setup: issues %+v, stepOrder %q", issues, saved.StepOrder)
	}
	tm, issues := ToTemplate(reordered(t, chainDefault...), saved)
	if len(issues) > 0 || tm.StepOrder != "" {
		t.Errorf("default chain over a saved reorder: issues %+v, stepOrder %q, want none and \"\"", issues, tm.StepOrder)
	}
}

// A reordered flow with a step's own failure choice is a check-shaped flow: its paths carry the order too,
// so the job log follows the workflow.
func TestReorderedBranchPathKeepsItsOrder(t *testing.T) {
	d := reordered(t, chainAudioFirst...)
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeConvert {
			d.Nodes[i].OnError = &ErrorPolicy{Action: "retry", Max: 2, BackoffSeconds: 30, Then: "skip"}
		}
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || tm.Kind != rules.KindBranches || tm.Branches == nil || len(*tm.Branches) == 0 {
		t.Fatalf("issues %+v, template %+v", issues, tm)
	}
	const want = "Audio,Subtitles,File type,Convert video"
	found := false
	for _, b := range *tm.Branches {
		if b.Then == nil {
			continue
		}
		found = true
		if b.Then.StepOrder != want {
			t.Errorf("branch %v: stepOrder %q, want %q", b.Path, b.Then.StepOrder, want)
		}
	}
	if !found {
		t.Fatal("no branch converts: the probe is not testing the path")
	}
	p := tm.Compile(video(1920, 1080))
	if got := strings.Join(labels(p), ","); got != want {
		t.Errorf("the job log's sections %q, want the workflow's order %q (skip %q)", got, want, p.Skip)
	}
}

// A check's yes path reordered: the job log of the 4K file follows the path's order.
func TestReorderedCheckPathCompilesInItsOrder(t *testing.T) {
	d, check := branchDoc(nil)
	byType := map[string]Node{}
	for _, n := range d.Nodes[2:] {
		byType[n.Type] = n
	}
	var order []Node
	for _, ty := range chainAudioFirst[1:] {
		order = append(order, byType[ty])
	}
	d.Nodes = append([]Node{d.Nodes[0], check}, order...)
	d.Edges = []Edge{{ID: NewID(), From: d.Nodes[0].ID, FromPort: "success", To: check.ID, ToPort: "in"},
		{ID: NewID(), From: check.ID, FromPort: "match", To: order[0].ID, ToPort: "in"}}
	end := Node{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": "Not 4K: left for later."}}
	d.Nodes = append(d.Nodes, end)
	d.Edges = append(d.Edges, chainEdges(order)...)
	d.Edges = append(d.Edges, Edge{ID: NewID(), From: check.ID, FromPort: "else", To: end.ID, ToPort: "in"})
	if r := Validate(d); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || tm.Kind != rules.KindBranches {
		t.Fatalf("issues %+v, kind %s", issues, tm.Kind)
	}
	p := tm.Compile(video(3840, 2160))
	const want = "Check the file,Audio,Subtitles,File type,Convert video"
	if p.Skip != "" || strings.Join(labels(p), ",") != want {
		t.Errorf("the 4K file's sections %v (skip %q), want %q", labels(p), p.Skip, want)
	}
}
