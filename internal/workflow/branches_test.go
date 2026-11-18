// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// segment is a straight workflow's steps after the input, with new ids (one path's conversion).
func segment(t rules.Template) []Node {
	d := FromTemplate(t, nil)
	var out []Node
	for _, n := range d.Nodes[1:] {
		n.ID = NewID()
		out = append(out, n)
	}
	return out
}

// branchDoc: the library's files → "Is it 4K?" → Yes: the 4K conversion; No: what no gets.
func branchDoc(no []Node) (Doc, Node) {
	d := FromTemplate(rules.SliderTemplate(3), nil)
	input := d.Nodes[0]
	check := Node{ID: NewID(), Type: TypeCheck, TypeVersion: 1, Name: "Is it 4K?", Enabled: true, Config: map[string]any{"resolution": 2160}}
	yes := segment(rules.SliderTemplate(4))
	nodes := append([]Node{input, check}, yes...)
	nodes = append(nodes, no...)
	edges := []Edge{{ID: NewID(), From: input.ID, FromPort: "success", To: check.ID, ToPort: "in"},
		{ID: NewID(), From: check.ID, FromPort: "match", To: yes[0].ID, ToPort: "in"}}
	edges = append(edges, chainEdges(yes)...)
	if len(no) > 0 {
		edges = append(edges, Edge{ID: NewID(), From: check.ID, FromPort: "else", To: no[0].ID, ToPort: "in"})
		edges = append(edges, chainEdges(no)...)
	}
	return Doc{SchemaVersion: d.SchemaVersion, Nodes: nodes, Edges: edges}, check
}

func video(w, h int) media.Info {
	return media.Info{Container: "matroska,webm", Ext: ".mkv", VideoCodec: "h264", BitrateKbps: 20000, DurationSec: 60, SizeBytes: 150 << 20,
		Streams: []media.Stream{{Type: "video", Codec: "h264", Width: w, Height: h, FrameRate: 24, PixFmt: "yuv420p"},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, BitrateKbps: 128, Language: "eng"}}}
}

// V6a: a file goes down the path its answers lead to, and the timeline says why in design's words.
func TestBranchesRoute(t *testing.T) {
	end := Node{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": "Not 4K: left for later."}}
	for _, c := range []struct {
		name string
		no   []Node
	}{{"a conversion", segment(rules.SliderTemplate(2))}, {"left alone", []Node{end}}} {
		d, check := branchDoc(c.no)
		if r := Validate(d); r.Errors() {
			t.Fatalf("%s: %+v", c.name, r.Issues)
		}
		tm, issues := ToTemplate(d, rules.Template{})
		if len(issues) > 0 || tm.Kind != rules.KindBranches || tm.Branches == nil || len(*tm.Branches) != 2 {
			t.Fatalf("%s: %+v %v", c.name, tm, issues)
		}
		p := tm.Compile(video(3840, 2160))
		if p.Skip != "" || len(p.Decisions) != 1 || p.Decisions[0].Words != "Is it 4K? → Yes (resolution is 2160p)" || p.Decisions[0].Port != "match" ||
			p.Decisions[0].NodeID != check.ID || len(p.Path) < 3 {
			t.Errorf("%s, 4K: %+v", c.name, p)
		}
		if want := rules.SliderTemplate(4).Compile(video(3840, 2160)); p.Video.CRF != want.Video.CRF { // the Yes path's own settings
			t.Errorf("%s, 4K: CRF %d, want the quality-4 path's %d", c.name, p.Video.CRF, want.Video.CRF)
		}
		hd := tm.Compile(video(1920, 1080))
		if hd.Decisions[0].Words != "Is it 4K? → No (resolution is 1080p)" || hd.Decisions[0].Port != "else" {
			t.Errorf("%s, 1080p: %+v", c.name, hd.Decisions)
		}
		if c.name == "left alone" && (hd.SkipCode != rules.SkipLeftByWorkflow || hd.Skip != "not 4K: left for later") {
			t.Errorf("left alone: %q %q", hd.SkipCode, hd.Skip)
		}
		if c.name == "a conversion" && hd.Video.CRF != rules.SliderTemplate(2).Compile(video(1920, 1080)).Video.CRF {
			t.Errorf("1080p: CRF %d", hd.Video.CRF)
		}
	}
}

// Every port of a check leads somewhere (design 02:05Z): an unconnected one is an error with its code,
// node and port, and design's words; so are a check with no condition, a check among the steps that
// convert, and a path that never saves.
func TestBranchesValidation(t *testing.T) {
	issue := func(d Doc, code string) Issue {
		t.Helper()
		_, issues := ToTemplate(d, rules.Template{})
		for _, i := range issues {
			if i.Code == code {
				return i
			}
		}
		t.Fatalf("no %s in %+v", code, issues)
		return Issue{}
	}
	d, check := branchDoc(nil) // No leads nowhere
	if i := issue(d, "unconnected_port"); i.NodeID != check.ID || i.Port != "else" || i.Severity != "error" ||
		i.Message != "'Is it 4K?' has nothing connected to No. Connect a step, or leave the file alone there." {
		t.Errorf("%+v", i)
	}
	d, check = branchDoc([]Node{{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{}}})
	for i := range d.Nodes {
		if d.Nodes[i].ID == check.ID {
			d.Nodes[i].Config = map[string]any{}
		}
	}
	if i := issue(d, "empty_check"); i.NodeID != check.ID || !strings.Contains(i.Message, "has no condition") {
		t.Errorf("%+v", i)
	}
	no := segment(rules.SliderTemplate(2))
	no = no[:len(no)-1] // without "Save the result"
	d, _ = branchDoc(no)
	if i := issue(d, "path_without_save"); i.NodeID != no[len(no)-1].ID {
		t.Errorf("%+v", i)
	}
	no = segment(rules.SliderTemplate(2)) // a check among the steps that convert
	inner := Node{ID: NewID(), Type: TypeCheck, TypeVersion: 1, Name: "Inner", Enabled: true, Config: map[string]any{"hdr": "yes"}}
	no = append(no[:2], append([]Node{inner}, no[2:]...)...)
	d, _ = branchDoc(no)
	if i := issue(d, "branch_in_conversion"); i.NodeID != inner.ID {
		t.Errorf("%+v", i)
	}
	no = segment(rules.SliderTemplate(2))[1:] // a path without "Leave some files alone": the review's case names it
	skips, _ := Definition(TypeSkips)
	d, _ = branchDoc(no)
	if i := issue(d, "not_runnable_yet"); !strings.HasPrefix(i.Message, "This path converts without '"+skips.Name+"'.") {
		t.Errorf("%+v", i)
	}
}

// The step definitions give each output port its own words (ui-shop 02:05Z).
func TestCheckPortsHaveWords(t *testing.T) {
	d, ok := Definition(TypeCheck)
	if !ok || len(d.Outputs) != 2 || d.Outputs[0].Name != "match" || d.Outputs[0].Label != "Yes" || d.Outputs[1].Name != "else" || d.Outputs[1].Label != "No" {
		t.Errorf("%+v", d.Outputs)
	}
	if e, ok := Definition(TypeEnd); !ok || len(e.Outputs) != 0 || e.Name != "Leave the file alone" {
		t.Errorf("%+v", e)
	}
}

// errorDoc: a straight workflow (quality 5) whose "Convert video" error port leads to the nodes in to.
func errorDoc(to []Node) (Doc, Node) {
	d := FromTemplate(rules.SliderTemplate(5), nil)
	var conv Node
	for _, n := range d.Nodes {
		if n.Type == TypeConvert {
			conv = n
		}
	}
	if len(to) > 0 {
		d.Nodes = append(d.Nodes, to...)
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: conv.ID, FromPort: "error", To: to[0].ID, ToPort: "in"})
		d.Edges = append(d.Edges, chainEdges(to)...)
	}
	return d, conv
}

// V6b: what a path does when converting doesn't work, from its error port's link or its onError.
func TestFailurePoliciesCompile(t *testing.T) {
	failure := func(d Doc) *rules.Failure {
		t.Helper()
		tm, issues := ToTemplate(d, rules.Template{})
		if len(issues) > 0 || tm.Branches == nil || len(*tm.Branches) != 1 || (*tm.Branches)[0].OnFailure == nil {
			t.Fatalf("%+v %v", tm, issues)
		}
		return (*tm.Branches)[0].OnFailure.Convert
	}
	other := segment(rules.SliderTemplate(1))
	d, conv := errorDoc(other)
	if f := failure(d); f.Action != "fallback" || f.NodeID != conv.ID || f.Fallback == nil || f.Fallback.Then == nil || len(f.Fallback.Path) == 0 {
		t.Errorf("fallback %+v", f)
	}
	d, _ = errorDoc([]Node{{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": "Too hard to convert."}}})
	if f := failure(d); f.Action != "skip" || f.SkipWords != "Too hard to convert." {
		t.Errorf("skip %+v", f)
	}
	d, conv = errorDoc(nil) // a straight workflow with its own choice: it runs as a path with that choice
	for i := range d.Nodes {
		if d.Nodes[i].ID == conv.ID {
			d.Nodes[i].OnError = &ErrorPolicy{Action: "retry", Max: 2, BackoffSeconds: 30, Then: "skip"}
		}
	}
	if f := failure(d); f.Action != "retry" || f.Max != 2 || f.BackoffSeconds != 30 || f.Then != "skip" {
		t.Errorf("retry %+v", f)
	}
	issue := func(d Doc, code string) {
		t.Helper()
		_, issues := ToTemplate(d, rules.Template{})
		for _, i := range issues {
			if i.Code == code {
				return
			}
		}
		t.Errorf("no %s in %+v", code, issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID == conv.ID {
			d.Nodes[i].OnError = &ErrorPolicy{Action: "retry", Max: 11}
		}
	}
	issue(d, "invalid_config")
	d, conv = errorDoc(segment(rules.SliderTemplate(1))) // a link and a different own choice
	for i := range d.Nodes {
		if d.Nodes[i].ID == conv.ID {
			d.Nodes[i].OnError = &ErrorPolicy{Action: "skip"}
		}
	}
	issue(d, "error_policy_conflict")
	check := Node{ID: NewID(), Type: TypeCheck, TypeVersion: 1, Name: "Is it big?", Enabled: true, Config: map[string]any{"size": 1000}}
	d, _ = errorDoc([]Node{check})
	issue(d, "fallback_must_convert")
}
