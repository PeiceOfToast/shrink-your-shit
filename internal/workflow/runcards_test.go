// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func node(typ, name string, config map[string]any) Node {
	return Node{ID: NewID(), Type: typ, TypeVersion: 1, Name: name, Enabled: true, Config: config}
}

func byID(d Doc, id string) Node {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

func link(from, port string, to string) Edge {
	return Edge{ID: NewID(), From: from, FromPort: port, To: to, ToPort: "in"}
}

// runDoc: Library files → Check the file name (Remux?) → Yes: Set a variable, Add a note, the conversion;
// No: Stop as failed.
func runDoc() (Doc, map[string]Node) {
	d := FromTemplate(rules.SliderTemplate(3), nil)
	input := d.Nodes[0]
	conv := segment(rules.SliderTemplate(4))
	q := node(TypeCheckName, "Is it a remux?", map[string]any{"pattern": "remux", "match": "contains"})
	set := node(TypeSetVariable, "", map[string]any{"name": "profile", "value": "remux of {{title}}"})
	note := node(TypeNote, "", map[string]any{"text": "Converting {{title}} as {{profile}}"})
	fail := node(TypeFail, "", map[string]any{"reason": "Not a remux: nothing to gain."})
	nodes := append([]Node{input, q, set, note, fail}, conv...)
	edges := []Edge{link(input.ID, "success", q.ID), link(q.ID, "match", set.ID), link(set.ID, "success", note.ID), link(note.ID, "success", conv[0].ID), link(q.ID, "else", fail.ID)}
	edges = append(edges, chainEdges(conv)...)
	return Doc{SchemaVersion: SchemaVersion, Nodes: nodes, Edges: edges, Variables: []Variable{}}, map[string]Node{"input": input, "q": q, "set": set, "note": note, "fail": fail, "conv": conv[0]}
}

// FB1b: questions of every kind, actions and the fail end compile into paths with their cards.
func TestRunCardsCompile(t *testing.T) {
	d, by := runDoc()
	r := Validate(d)
	for _, i := range r.Issues { // until FB1c the three cards are not_runnable_yet; nothing else may be wrong
		if i.Code != "not_runnable_yet" || !NotYet[byID(d, i.NodeID).Type] {
			t.Errorf("%+v", i)
		}
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || tm.Branches == nil || len(*tm.Branches) != 2 {
		t.Fatalf("%+v %v", tm, issues)
	}
	yes, no := (*tm.Branches)[0], (*tm.Branches)[1]
	if yes.Then == nil || len(yes.Checks) != 1 || yes.Checks[0].Check.Kind != "name" || yes.Checks[0].Check.Pattern != "remux" || yes.Checks[0].Port != "match" {
		t.Errorf("yes path: %+v", yes)
	}
	if len(yes.Before) != 2 || yes.Before[0].Type != TypeSetVariable || yes.Before[0].NodeID != by["set"].ID || yes.Before[1].Type != TypeNote || yes.Before[1].Config["text"] != "Converting {{title}} as {{profile}}" {
		t.Errorf("before cards: %+v", yes.Before)
	}
	if no.Then != nil || !no.Fail || no.End != "Not a remux: nothing to gain." || no.Checks[0].Port != "else" || len(no.Before) != 0 {
		t.Errorf("no path: %+v", no)
	}
	// routing: a remux takes the Yes path with its cards; another file fails with the words
	info := video(1920, 1080)
	p := tm.CompileWith(info, rules.Facts{Path: "/m/Harrow.Lane.2019.Remux.mkv"})
	if p.Skip != "" || len(p.Before) != 2 || p.Fail || len(p.Decisions) != 1 || !strings.Contains(p.Decisions[0].Words, "Yes") {
		t.Errorf("remux plan: skip %q before %d fail %v decisions %+v", p.Skip, len(p.Before), p.Fail, p.Decisions)
	}
	p = tm.CompileWith(info, rules.Facts{Path: "/m/Harrow.Lane.2019.WEB.mkv"})
	if !p.Fail || p.Skip == "" || p.SkipCode != rules.SkipLeftByWorkflow || len(p.Before) != 0 {
		t.Errorf("web plan: %+v", p)
	}
	// Compile without facts still routes by the file's facts (an old caller)
	if p := tm.Compile(info); !p.Fail {
		t.Errorf("no facts: %+v", p)
	}
}

// The steps view's rules (W11, FB1b): run cards can be added and moved; the conversion's steps stay in place.
func TestRunCardsCanBeAdded(t *testing.T) {
	d, by := runDoc()
	r := Validate(d)
	for _, typ := range append(slices.Clone(Questions), Actions...) {
		a := r.AddSteps[typ]
		if !a.Addable || a.After != by["note"].ID {
			t.Errorf("%s: %+v (want addable after the note)", typ, a)
		}
	}
	for _, typ := range Ends { // an end has no Next: the graph places it
		if a := r.AddSteps[typ]; a.Addable || !strings.Contains(a.Reason, "graph") {
			t.Errorf("%s: %+v", typ, a)
		}
	}
	for _, typ := range PresetOrder[1:] {
		if r.AddSteps[typ].Addable {
			t.Errorf("%s: addable, but the conversion's steps are set until W10", typ)
		}
	}
	if r.AddSteps[TypeLegacy].Addable {
		t.Error("the legacy step is addable")
	}
	if r.StepPlaces[by["q"].ID].FixedPlace || r.StepPlaces[by["set"].ID].FixedPlace || !r.StepPlaces[by["conv"].ID].FixedPlace {
		t.Errorf("places: %+v", r.StepPlaces)
	}
	// a new workflow: run cards go right after Library files
	p := Preset()
	if a := Validate(p).AddSteps[TypeCheckName]; !a.Addable || a.After != p.Nodes[0].ID {
		t.Errorf("preset: %+v", a)
	}
}

// A question, an action or an end after the steps that convert is refused while editing (wrong_phase)
// and at compile (branch_in_conversion).
func TestRunCardsKeepTheirPhase(t *testing.T) {
	d := FromTemplate(rules.SliderTemplate(3), nil)
	var conv, audio Node
	for _, n := range d.Nodes {
		switch n.Type {
		case TypeConvert:
			conv = n
		case TypeAudio:
			audio = n
		}
	}
	late := node(TypeNote, "", map[string]any{"text": "too late"})
	d.Nodes = append(d.Nodes, late)
	for i := range d.Edges { // convert → note → audio
		if d.Edges[i].From == conv.ID {
			d.Edges[i].To = late.ID
		}
	}
	d.Edges = append(d.Edges, link(late.ID, "success", audio.ID))
	r := Validate(d)
	codes := []string{}
	for _, i := range r.Issues {
		codes = append(codes, i.Code)
	}
	if !slices.Contains(codes, "wrong_phase") {
		t.Errorf("no wrong_phase in %v", codes)
	}
	for _, i := range r.Issues {
		if i.Code == "wrong_phase" && (i.NodeID != late.ID || !strings.Contains(i.Message, "Convert video")) {
			t.Errorf("%+v", i)
		}
	}
	// the compile answers with an issue too, and never recurses (review2-531 P1): a note, a new question kind and an
	// end between Convert video and Audio each come back as branch_in_conversion
	for _, typ := range []string{TypeNote, TypeCheckName, TypeFail, TypeSetVariable} {
		d := FromTemplate(rules.SliderTemplate(3), nil)
		var conv, audio Node
		for _, n := range d.Nodes {
			switch n.Type {
			case TypeConvert:
				conv = n
			case TypeAudio:
				audio = n
			}
		}
		mid := node(typ, "", map[string]any{"text": "x", "pattern": "x", "name": "v"})
		d.Nodes = append(d.Nodes, mid)
		for i := range d.Edges {
			if d.Edges[i].From == conv.ID {
				d.Edges[i].To = mid.ID
			}
		}
		port := "success"
		if IsQuestion(mid.Type) {
			port = "match"
		}
		if !IsEnd(mid.Type) {
			d.Edges = append(d.Edges, link(mid.ID, port, audio.ID))
		}
		_, issues := ToTemplate(d, rules.Template{}) // the draft shape the workflow list compiles (v2workflows workflowViewWith)
		if !slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == "branch_in_conversion" && i.NodeID == mid.ID }) {
			t.Errorf("%s inside the conversion: %+v", typ, issues)
		}
	}
	// a fail end on the conversion's error port is fine: the failure policy says stop
	d2, conv2 := errorDoc([]Node{node(TypeFail, "", nil)})
	tm, issues := ToTemplate(d2, rules.Template{})
	if len(issues) > 0 || tm.Branches == nil || (*tm.Branches)[0].OnFailure == nil || (*tm.Branches)[0].OnFailure.Convert.Action != "stop" || (*tm.Branches)[0].OnFailure.Convert.NodeID != conv2.ID {
		t.Errorf("fail on error port: %+v %v", tm, issues)
	}
}

// Set a variable publishes a name later fields and questions may use; the server refuses bad names and
// a question about a name nothing sets.
func TestPublishedVariables(t *testing.T) {
	d, by := runDoc()
	if !slices.Contains(PublishedNames(d), "profile") {
		t.Errorf("published: %v", PublishedNames(d))
	}
	for _, i := range Validate(d).Issues { // the note reads {{profile}}: known because the step before sets it
		if i.Code == "unknown_variable" {
			t.Fatalf("%+v", i)
		}
	}
	ask := node(TypeCheckVariable, "", map[string]any{"variable": "profile", "op": "equals", "value": "remux"})
	d.Nodes = append(d.Nodes, ask)
	if issues := CheckPublished(d); len(issues) != 0 {
		t.Errorf("a published name is unknown: %+v", issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID == ask.ID {
			d.Nodes[i].Config["variable"] = "profil"
		}
	}
	issues := CheckPublished(d)
	if len(issues) != 1 || issues[0].Code != "unknown_variable" || issues[0].Suggest != "profile" || issues[0].NodeID != ask.ID {
		t.Errorf("typo: %+v", issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID == by["set"].ID {
			d.Nodes[i].Config["name"] = "title" // the server sets it
		}
	}
	issues = CheckPublished(d)
	if !slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == "builtin_variable_name" }) {
		t.Errorf("builtin: %+v", issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID == by["set"].ID {
			d.Nodes[i].Config["name"] = "Bad Name"
		}
	}
	issues = CheckPublished(d)
	if !slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == "invalid_variable_name" }) {
		t.Errorf("bad name: %+v", issues)
	}
}

// A regular expression that can't be read is refused while editing (review2-531 P3).
func TestBadRegexIsRefused(t *testing.T) {
	d, by := runDoc()
	for i := range d.Nodes {
		if d.Nodes[i].ID == by["q"].ID {
			d.Nodes[i].Config = map[string]any{"pattern": "(", "match": "regex"}
		}
	}
	r := Validate(d)
	if !slices.ContainsFunc(r.Issues, func(i Issue) bool {
		return i.Code == "invalid_config" && i.NodeID == by["q"].ID && i.Field == "pattern"
	}) {
		t.Errorf("%+v", r.Issues)
	}
}

// Every run card has a summary in plain words, and a question's says what it asks.
func TestRunCardSummaries(t *testing.T) {
	cases := []struct {
		n    Node
		want string
	}{
		{node(TypeCheck, "", map[string]any{"resolution": 2160, "hdr": "yes"}), "Only if the resolution is at least 2160p and it is HDR"},
		{node(TypeCheck, "", map[string]any{"audioCodec": []any{"dts", "truehd"}, "minMinutes": 90}), "Only if some audio is DTS or TRUEHD and it runs at least 90 min"},
		{node(TypeCheckName, "", map[string]any{"pattern": "Remux", "match": "contains"}), "Only if the file name contains “Remux”"},
		{node(TypeCheckName, "", map[string]any{"pattern": "*.1080p.*", "match": "glob", "where": "path"}), "Only if the path matches “*.1080p.*”"},
		{node(TypeCheckAge, "", map[string]any{"olderDays": 30}), "Only if last changed at least 30 days ago"},
		{node(TypeCheckVariable, "", map[string]any{"variable": "profile", "op": "equals", "value": "remux"}), "Only if {{profile}} is “remux”"},
		{node(TypeCheckLibrary, "", map[string]any{"libraries": []any{"1", "2"}}), "Only if the file is from one of 2 libraries"},
		{node(TypeCheckHardLinks, "", nil), "Only if the file shares its data with another name"},
		{node(TypeSetVariable, "", map[string]any{"name": "profile", "value": "remux"}), "Set {{profile}} = “remux”"},
		{node(TypeNote, "", map[string]any{"text": "hello"}), "Note: “hello”"},
		{node(TypeFail, "", map[string]any{"reason": "No."}), "Failed: No."},
		{node(TypeEnd, "", map[string]any{"reason": "Left for later."}), "Left for later."},
	}
	for _, c := range cases {
		if got := summary(c.n); got != c.want {
			t.Errorf("%s: %q, want %q", c.n.Type, got, c.want)
		}
	}
	// the registry's new conditions reach the question
	n := node(TypeCheck, "Ten-bit?", map[string]any{"bitDepth": 10, "subtitleLanguage": []any{"eng"}})
	q := questionOf(n)
	if q.BitDepth != 10 || len(q.SubtitleLanguages) != 1 || q.Name != "Ten-bit?" || q.Kind != "" {
		t.Errorf("%+v", q)
	}
	if yes, _ := q.Ask(media.Info{Streams: []media.Stream{{Type: "video", Width: 1920, Height: 1080, BitDepth: 10}, {Type: "subtitle", Language: "eng"}}}, rules.Facts{}); !yes {
		t.Error("ten-bit with an English subtitle should answer Yes")
	}
}
