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
	if r.Errors() { // FB1c: every card the registry offers runs, so the document publishes
		t.Fatalf("%+v", r.Issues)
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

// A run card on a conversion step's skipped or error (Didn't work) port is refused at publish, except an end on
// Convert video's or Check the result's error port, which becomes the path's failure policy (stop / skip).
func TestRunCardsOnFailurePortsAreRefused(t *testing.T) {
	for _, port := range []string{"skipped", "error"} {
		for _, typ := range []string{TypeNote, TypeSetVariable, TypeCheckName, TypeFail, TypeEnd} {
			d := FromTemplate(rules.SliderTemplate(3), nil)
			var conv Node
			for _, n := range d.Nodes {
				if n.Type == TypeConvert {
					conv = n
				}
			}
			card := node(typ, "", map[string]any{"text": "x", "name": "v", "pattern": "x", "reason": "no"})
			d.Nodes = append(d.Nodes, card)
			d.Edges = append(d.Edges, link(conv.ID, port, card.ID))
			if IsQuestion(typ) { // its answers must lead somewhere
				end := node(TypeEnd, "", nil)
				d.Nodes = append(d.Nodes, end)
				d.Edges = append(d.Edges, link(card.ID, "match", end.ID), link(card.ID, "else", end.ID))
			}
			tm, issues := ToTemplate(d, rules.Template{})
			allowed := port == "error" && IsEnd(typ)
			switch {
			case allowed && len(issues) > 0:
				t.Errorf("%s on %s: %+v (an end there is the failure policy)", typ, port, issues)
			case allowed:
				f := (*tm.Branches)[0].OnFailure.Convert
				want := map[string]string{TypeFail: "stop", TypeEnd: "skip"}[typ]
				if f == nil || f.Action != want {
					t.Errorf("%s on %s: policy %+v, want %s", typ, port, f, want)
				}
			case !allowed:
				if !slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == "not_runnable_yet" || i.Code == "fallback_must_convert" }) {
					t.Errorf("%s on %s: not refused: %+v", typ, port, issues)
				}
				if r := Validate(d); !r.Errors() {
					t.Errorf("%s on %s: publish would be allowed", typ, port)
				}
			}
		}
	}
}

// An action turned off is passed over (the path goes on without it); a question can't be turned off.
func TestRunCardsTurnedOff(t *testing.T) {
	d, by := runDoc()
	for i := range d.Nodes {
		if d.Nodes[i].ID == by["note"].ID {
			d.Nodes[i].Enabled = false
		}
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || len((*tm.Branches)[0].Before) != 1 || (*tm.Branches)[0].Before[0].Type != TypeSetVariable {
		t.Errorf("%+v %v", (*tm.Branches)[0].Before, issues)
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID == by["q"].ID {
			d.Nodes[i].Enabled = false
		}
	}
	if r := Validate(d); !slices.ContainsFunc(r.Issues, func(i Issue) bool {
		return i.Code == "invalid_config" && i.NodeID == by["q"].ID && strings.Contains(i.Message, "can't be turned off")
	}) {
		t.Errorf("%+v", r.Issues)
	}
}

// The steps view's rules (W11, FB1b): run cards can be added and moved; the conversion's steps stay in place.
func TestRunCardsCanBeAdded(t *testing.T) {
	d, by := runDoc()
	r := Validate(d)
	save := d.Nodes[len(d.Nodes)-1]
	for _, typ := range append(slices.Clone(Questions), Actions...) {
		a := r.AddSteps[typ]
		if !slices.Contains(definitionPhase(typ), PhaseBefore) { // FB2a: an action on the saved result goes after the save
			if !a.Addable || a.After != save.ID {
				t.Errorf("%s: %+v (want addable after the save)", typ, a)
			}
			continue
		}
		if !a.Addable || a.After != by["note"].ID {
			t.Errorf("%s: %+v (want addable after the note)", typ, a)
		}
	}
	// with a checksum behind the save, the next after card goes behind the checksum
	sum := node(TypeChecksum, "", map[string]any{})
	d.Nodes = append(d.Nodes, sum)
	d.Edges = append(d.Edges, link(save.ID, "success", sum.ID))
	if a := Validate(d).AddSteps[TypeCopy]; !a.Addable || a.After != sum.ID {
		t.Errorf("copy after the checksum: %+v", a)
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
	for _, i := range Validate(d).Issues { // the note reads {{profile}}: known because the step before sets it; nothing may object (the gate is empty since FB1c)
		t.Fatalf("%+v", i)
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

// FB2a: the actions after the save compile into the path's After list in order (a turned-off one left
// out), an after-only action before the conversion or a before-only one after the save is wrong_phase, and
// the checksum's published names are known to the cards after it.
func TestAfterCardsCompile(t *testing.T) {
	d, by := runDoc()
	save := d.Nodes[len(d.Nodes)-1]
	if save.Type != TypeCommit {
		t.Fatalf("the last node is %s, want the save", save.Type)
	}
	sum := node(TypeChecksum, "", map[string]any{"kind": "sha1"})
	side := node(TypeSidecar, "", map[string]any{"name": "{{title}}.sha1", "text": "{{checksum}}"})
	off := node(TypeNotify, "", map[string]any{"text": "{{title}} done"})
	off.Enabled = false
	copyTo := node(TypeCopy, "", map[string]any{"folder": "/backup/{{library}}"})
	d.Nodes = append(d.Nodes, sum, side, off, copyTo)
	d.Edges = append(d.Edges, link(save.ID, "success", sum.ID), link(sum.ID, "success", side.ID), link(side.ID, "success", off.ID), link(off.ID, "success", copyTo.ID))
	if r := Validate(d); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || tm.Branches == nil {
		t.Fatalf("%+v %v", tm, issues)
	}
	yes := (*tm.Branches)[0]
	if len(yes.After) != 3 || yes.After[0].Type != TypeChecksum || yes.After[1].Type != TypeSidecar || yes.After[2].Type != TypeCopy || yes.After[1].Config["text"] != "{{checksum}}" {
		t.Errorf("after cards: %+v", yes.After)
	}
	if yes.Path[len(yes.Path)-1] != copyTo.ID || len(yes.Before) != 2 {
		t.Errorf("path %v, before %d", yes.Path, len(yes.Before))
	}
	for _, c := range yes.After {
		if c.Name == "" {
			t.Errorf("a card without a name: %+v", c)
		}
	}
	// the plan carries them, and a plan without any is unchanged on the wire
	plan := tm.CompileWith(media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}, rules.Facts{Path: "/films/Harrow.Lane.Remux.mkv"})
	if len(plan.After) != 3 {
		t.Errorf("plan.After: %+v", plan.After)
	}

	// an after-only card before the conversion: wrong_phase names it, and the compile refuses too
	d2, by2 := runDoc()
	early := node(TypeChecksum, "", map[string]any{})
	d2.Nodes = append(d2.Nodes, early)
	for i, e := range d2.Edges {
		if e.From == by2["note"].ID {
			d2.Edges[i].To = early.ID
		}
	}
	d2.Edges = append(d2.Edges, link(early.ID, "success", by2["conv"].ID))
	if !hasIssue(Validate(d2).Issues, "wrong_phase", early.ID) {
		t.Errorf("an early checksum: %+v", Validate(d2).Issues)
	}
	if _, is := ToTemplate(d2, rules.Template{}); !hasIssue(is, "wrong_phase", early.ID) {
		t.Errorf("compile of an early checksum: %+v", is)
	}
	// a question after the save: wrong_phase too (a variable or a note may sit on either side: both phases)
	d3, _ := runDoc()
	save3 := d3.Nodes[len(d3.Nodes)-1]
	late := node(TypeCheckName, "", map[string]any{"pattern": "x", "match": "contains"})
	fail3 := node(TypeFail, "", map[string]any{"reason": "no"})
	d3.Nodes = append(d3.Nodes, late, fail3)
	d3.Edges = append(d3.Edges, link(save3.ID, "success", late.ID), link(late.ID, "match", fail3.ID), link(late.ID, "else", fail3.ID))
	if !hasIssue(Validate(d3).Issues, "wrong_phase", late.ID) {
		t.Errorf("a late question: %+v", Validate(d3).Issues)
	}
	if _, is := ToTemplate(d3, rules.Template{}); !hasIssue(is, "wrong_phase", late.ID) {
		t.Errorf("compile of a late question: %+v", is)
	}
	d5, _ := runDoc() // and a note after the save is fine, and runs there
	save5 := d5.Nodes[len(d5.Nodes)-1]
	note5 := node(TypeNote, "", map[string]any{"text": "{{title}} is saved"})
	d5.Nodes = append(d5.Nodes, note5)
	d5.Edges = append(d5.Edges, link(save5.ID, "success", note5.ID))
	if r := Validate(d5); r.Errors() {
		t.Errorf("a late note: %+v", r.Issues)
	}
	if tm5, is := ToTemplate(d5, rules.Template{}); len(is) > 0 || len((*tm5.Branches)[0].After) != 1 {
		t.Errorf("compile of a late note: %+v %+v", is, tm5.Branches)
	}
	// an after card's error port leads nowhere yet
	d4, _ := runDoc()
	save4 := d4.Nodes[len(d4.Nodes)-1]
	s4, n4 := node(TypeChecksum, "", map[string]any{}), node(TypeNotify, "", map[string]any{"text": "x"})
	d4.Nodes = append(d4.Nodes, s4, n4)
	d4.Edges = append(d4.Edges, link(save4.ID, "success", s4.ID), link(s4.ID, "error", n4.ID))
	if _, is := ToTemplate(d4, rules.Template{}); !hasIssue(is, "not_runnable_yet", s4.ID) {
		t.Errorf("an after card's error port: %+v", is)
	}
	_ = by
}

func hasIssue(issues []Issue, code, nodeID string) bool {
	for _, i := range issues {
		if i.Code == code && i.NodeID == nodeID {
			return true
		}
	}
	return false
}

// FB2b: Run a command sits after the save and is refused by the policy until the Safety switch is on (the draft
// keeps it; the issue names the switch); Ask the downloader sits before the conversion and publishes its names.
func TestCommandNeedsTheSafetySwitch(t *testing.T) {
	d, by := runDoc()
	save := d.Nodes[len(d.Nodes)-1]
	cmd := node(TypeCommand, "", map[string]any{"program": "/usr/local/bin/after", "arguments": `"{{output_path}}" {{command_output}}`})
	look := node(TypeLookupDownloader, "", map[string]any{})
	d.Nodes = append(d.Nodes, cmd, look)
	d.Edges = append(d.Edges, link(save.ID, "success", cmd.ID))
	for i, e := range d.Edges { // the lookup goes between the note and the conversion
		if e.From == by["note"].ID {
			d.Edges[i].To = look.ID
		}
	}
	d.Edges = append(d.Edges, link(look.ID, "success", by["conv"].ID))
	if r := Validate(d); r.Errors() {
		t.Fatalf("allowed: %+v", r.Issues)
	}
	r := ValidateWith(d, Scopes{Policy: Policy{}})
	if !hasIssue(r.Issues, "commands_off", cmd.ID) || !r.Errors() {
		t.Errorf("off: %+v", r.Issues)
	}
	for _, i := range r.Issues {
		if i.Code == "commands_off" && !strings.Contains(i.Message, "Settings > Safety") {
			t.Errorf("the issue should name the switch: %q", i.Message)
		}
	}
	if r := ValidateWith(d, Scopes{Policy: Policy{CommandsAllowed: true}}); r.Errors() {
		t.Errorf("on: %+v", r.Issues)
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	yes := (*tm.Branches)[0]
	if len(yes.After) != 1 || yes.After[0].Type != TypeCommand || len(yes.Before) != 3 || yes.Before[2].Type != TypeLookupDownloader {
		t.Errorf("places: after %+v, before %+v", yes.After, yes.Before)
	}
	for _, name := range []string{"title_clean", "year", "season", "episode", "original_language", "command_output"} {
		if !slices.Contains(PublishedNames(d), name) {
			t.Errorf("%s is not published", name)
		}
	}
	// the lookup can't sit after the save; the command can't sit before the conversion
	d2, _ := runDoc()
	save2 := d2.Nodes[len(d2.Nodes)-1]
	late := node(TypeLookupDownloader, "", map[string]any{})
	d2.Nodes = append(d2.Nodes, late)
	d2.Edges = append(d2.Edges, link(save2.ID, "success", late.ID))
	if !hasIssue(Validate(d2).Issues, "wrong_phase", late.ID) {
		t.Errorf("a late lookup: %+v", Validate(d2).Issues)
	}
	if a := Validate(d).AddSteps[TypeCommand]; !a.Addable || a.After != cmd.ID {
		t.Errorf("add rule for a command: %+v", a)
	}
	if a := Validate(d).AddSteps[TypeLookupDownloader]; !a.Addable || a.After != look.ID {
		t.Errorf("add rule for a lookup: %+v", a)
	}
}

// FB2c: Has a tag is a question (before the conversion) and Tag the file an action that may sit on either side of
// the save; both have summaries, the action publishes {{tags}}, and a tag question with nothing to ask is caught.
func TestTagCards(t *testing.T) {
	d, by := runDoc()
	save := d.Nodes[len(d.Nodes)-1]
	tag := node(TypeTag, "", map[string]any{"add": "converted, {{profile}}", "remove": "needs review"})
	late := node(TypeTag, "", map[string]any{"add": "done"})
	ask := node(TypeCheckTag, "", map[string]any{"tags": "remux, needs review", "tagOp": "all"})
	leave := node(TypeEnd, "", map[string]any{"reason": "Not tagged."})
	d.Nodes = append(d.Nodes, tag, late, ask, leave)
	for i, e := range d.Edges { // input → ask → (yes) q …; note → tag → conv; save → late
		if e.From == by["input"].ID {
			d.Edges[i].To = ask.ID
		}
		if e.From == by["note"].ID {
			d.Edges[i].To = tag.ID
		}
	}
	d.Edges = append(d.Edges, link(ask.ID, "match", by["q"].ID), link(ask.ID, "else", leave.ID), link(tag.ID, "success", by["conv"].ID), link(save.ID, "success", late.ID))
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if s := r.StepSummaries[ask.ID]; s != "Only if the file has all of the tags remux, needs review" {
		t.Errorf("ask summary: %q", s)
	}
	if s := r.StepSummaries[tag.ID]; s != "Adds converted, {{profile}}; removes needs review" {
		t.Errorf("tag summary: %q", s)
	}
	if s := r.StepSummaries[late.ID]; s != "Adds done" {
		t.Errorf("late summary: %q", s)
	}
	if !slices.Contains(PublishedNames(d), "tags") {
		t.Error("tags is not published")
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	yes := (*tm.Branches)[0]
	if len(yes.Checks) != 2 || yes.Checks[0].Check.Kind != "tag" || yes.Checks[0].Check.TagOp != "all" || yes.Checks[0].Check.Tags != "remux, needs review" {
		t.Errorf("checks: %+v", yes.Checks)
	}
	if len(yes.Before) != 3 || yes.Before[2].Type != TypeTag || len(yes.After) != 1 || yes.After[0].Type != TypeTag {
		t.Errorf("cards: before %+v after %+v", yes.Before, yes.After)
	}
	// a tag question with nothing to ask
	ask.Config = map[string]any{"tags": " , "}
	d.Nodes[len(d.Nodes)-2] = ask
	if !Validate(d).Errors() {
		t.Error("an empty tag question should be refused")
	}
	// review2-542 P2: a Tag card with nothing to add or remove (or only separators) is refused at publish, naming
	// the field, and its summary never reads "Adds "
	ask.Config = map[string]any{"tags": "remux"}
	d.Nodes[len(d.Nodes)-2] = ask
	for _, cfg := range []map[string]any{{}, {"add": " , "}, {"add": "", "remove": ", ,"}} {
		tag.Config = cfg
		d.Nodes[len(d.Nodes)-4] = tag
		r := Validate(d)
		if !hasIssue(r.Issues, "invalid_config", tag.ID) {
			t.Errorf("%v: an empty Tag card should be refused: %+v", cfg, r.Issues)
		}
		if s := r.StepSummaries[tag.ID]; strings.HasPrefix(s, "Adds ") {
			t.Errorf("%v: summary %q", cfg, s)
		}
	}
	// review2-542 P3a: the limits the description names are the ones the code keeps
	if rules.MaxTags != 20 || rules.MaxTagLen != 40 {
		t.Errorf("limits: %d tags of %d", rules.MaxTags, rules.MaxTagLen)
	}
	def, _ := Definition(TypeTag)
	if !strings.Contains(def.Description, "40 characters") || !strings.Contains(def.Description, "at most 20 tags") {
		t.Errorf("the description names other limits: %q", def.Description)
	}
}

// FB4: Convert again later is an end that carries its wait and its tries into the branch; A share of files is a
// question; Wait is a before action whose settings are checked; summaries for all three.
func TestFlowCardsFB4(t *testing.T) {
	d, by := runDoc()
	again := node(TypeRequeue, "", map[string]any{"afterMin": float64(30), "maxTries": float64(3), "reason": "The download never finished"})
	d.Nodes = append(d.Nodes, again)
	for i, e := range d.Edges { // the No path tries again later instead of failing
		if e.From == by["q"].ID && e.FromPort == "else" {
			d.Edges[i].To = again.ID
		}
	}
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return n.ID == by["fail"].ID })
	share := node(TypeCheckRandom, "", map[string]any{"share": float64(25)})
	wait := node(TypeWait, "", map[string]any{"until": "02:00"})
	leave := node(TypeEnd, "", map[string]any{"reason": "Not this time."})
	d.Nodes = append(d.Nodes, share, wait, leave)
	for i, e := range d.Edges { // input → share → (yes) wait → q
		if e.From == by["input"].ID {
			d.Edges[i].To = share.ID
		}
	}
	d.Edges = append(d.Edges, link(share.ID, "match", wait.ID), link(wait.ID, "success", by["q"].ID), link(share.ID, "else", leave.ID))
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	for id, want := range map[string]string{again.ID: "Back in the queue for 30 minutes, at most 3 tries", share.ID: "Only for about 25% of files", wait.ID: "Waits in the queue until 02:00"} {
		if r.StepSummaries[id] != want {
			t.Errorf("summary %q, want %q", r.StepSummaries[id], want)
		}
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	var later *rules.Branch
	for i := range *tm.Branches {
		if b := &(*tm.Branches)[i]; b.Requeue != nil {
			later = b
		}
	}
	if later == nil || later.Requeue.AfterSec != 1800 || later.Requeue.MaxTries != 3 || later.End != "The download never finished" || !later.Fail || later.Then != nil {
		t.Errorf("the requeue branch: %+v", later)
	}
	yes := (*tm.Branches)[0]
	if len(yes.Checks) != 2 || yes.Checks[0].Check.Kind != "random" || yes.Checks[0].Check.Share != 25 || len(yes.Before) != 3 || yes.Before[0].Type != TypeWait {
		t.Errorf("yes path: checks %+v before %+v", yes.Checks, yes.Before)
	}
	plan := tm.CompileWith(media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}, rules.Facts{Path: "/films/Plain.mkv"})
	if plan.Requeue != nil && (plan.Requeue.MaxTries != 3 || plan.Skip != "The download never finished") {
		t.Errorf("a requeued plan: %+v", plan.Requeue)
	}
	// a Wait with nothing set, or a time it can't read, is refused
	wait.Config = map[string]any{}
	d.Nodes[len(d.Nodes)-2] = wait
	if !hasIssue(Validate(d).Issues, "invalid_config", wait.ID) {
		t.Error("an empty wait should be refused")
	}
	for _, bad := range []string{"2 am", "25:00", "2:00", "02:60", "0200"} { // review-548: refused at publish, not at run time
		wait.Config = map[string]any{"until": bad}
		d.Nodes[len(d.Nodes)-2] = wait
		if !hasIssue(Validate(d).Issues, "invalid_config", wait.ID) {
			t.Errorf("%q should be refused", bad)
		}
	}
	wait.Config = map[string]any{"until": "02:00"}
	d.Nodes[len(d.Nodes)-2] = wait
	if hasIssue(Validate(d).Issues, "invalid_config", wait.ID) {
		t.Error("02:00 is a time")
	}
	if a := Validate(d).AddSteps[TypeRequeue]; a.Addable {
		t.Error("an end is placed in the graph")
	}
}

// FB4b: "Hand to another workflow" compiles to a path that names the workflow; the policy's view of the
// other workflows refuses a hand-over to this workflow, to one that isn't there, to one never published,
// and to one that leads back here; with a good target the step's summary names it.
func TestHandToAnotherWorkflow(t *testing.T) {
	d, by := runDoc()
	goTo := node(TypeGoto, "", map[string]any{"workflow": "7"})
	d.Nodes = append(d.Nodes, goTo)
	for i, e := range d.Edges { // the No path hands the file on instead of failing
		if e.From == by["q"].ID && e.FromPort == "else" {
			d.Edges[i].To = goTo.ID
		}
	}
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return n.ID == by["fail"].ID })
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if r.StepSummaries[goTo.ID] != "Hands the file to another workflow" {
		t.Errorf("summary %q", r.StepSummaries[goTo.ID])
	}
	if a := r.AddSteps[TypeGoto]; a.Addable {
		t.Error("an end is placed in the graph")
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	var hand *rules.Branch
	for i := range *tm.Branches {
		if b := &(*tm.Branches)[i]; b.Goto != 0 {
			hand = b
		}
	}
	if hand == nil || hand.Goto != 7 || hand.GotoNode != goTo.ID || hand.Then != nil || hand.Fail || len(hand.Checks) != 1 || hand.Checks[0].Port != "else" || hand.Path[len(hand.Path)-1] != goTo.ID {
		t.Fatalf("the hand-over path: %+v", hand)
	}
	// no workflow chosen
	goTo.Config = map[string]any{}
	d.Nodes[len(d.Nodes)-1] = goTo
	if !hasIssue(Validate(d).Issues, "invalid_config", goTo.ID) {
		t.Error("a hand-over without a workflow is refused")
	}
	for _, bad := range []any{"", "0", "x", float64(7)} { // review2-579: the placeholder picked again, and other non-ids
		goTo.Config = map[string]any{"workflow": bad}
		d.Nodes[len(d.Nodes)-1] = goTo
		if !hasIssue(Validate(d).Issues, "invalid_config", goTo.ID) {
			t.Errorf("workflow %#v should be refused", bad)
		}
	}
	goTo.Config = map[string]any{"workflow": "7"}
	d.Nodes[len(d.Nodes)-1] = goTo
	// the other workflows, as the store would tell them: 7 is published and hands to 8; 8 hands back to 5 (this one); 9 is a draft
	others := map[int64]OtherWorkflow{7: {Name: "Big films", Published: true, HandsTo: []int64{8}}, 8: {Name: "Back", Published: true, HandsTo: []int64{5}}, 9: {Name: "Draft", Published: false}, 10: {Name: "By file", Published: true, FileValues: true}}
	look := func(id int64) (OtherWorkflow, bool) { o, ok := others[id]; return o, ok }
	code := func(self int64, target string) (string, string) {
		goTo.Config = map[string]any{"workflow": target}
		d.Nodes[len(d.Nodes)-1] = goTo
		res := ValidateWith(d, Scopes{Policy: Policy{CommandsAllowed: true, Workflows: look, SelfID: self}})
		for _, i := range res.Issues {
			if i.NodeID == goTo.ID {
				return i.Code, i.Message
			}
		}
		return "", res.StepSummaries[goTo.ID]
	}
	for _, c := range []struct {
		self                int64
		target, code, words string
	}{
		{7, "7", "handoff_loop", "hands the file back to this workflow"},
		{5, "7", "handoff_loop", "leads back to this workflow through 'Big films'"},
		{6, "7", "", "Hands the file to Big films"},
		{5, "9", "handoff_unpublished", "'Draft', which hasn't been published yet"},
		{5, "10", "handoff_file_values", "'By file', whose settings use the file's own values"},
		{5, "42", "invalid_config", "isn't there any more"},
	} {
		if got, words := code(c.self, c.target); got != c.code || !strings.Contains(words, c.words) {
			t.Errorf("self %d → %s: %q %q, want %q with %q", c.self, c.target, got, words, c.code, c.words)
		}
	}
	// no lookup (Validate alone, or a policy without one): the target is not checked
	goTo.Config = map[string]any{"workflow": "42"}
	d.Nodes[len(d.Nodes)-1] = goTo
	if res := ValidateWith(d, Scopes{Policy: Policy{CommandsAllowed: true}}); res.Errors() {
		t.Errorf("without a lookup: %+v", res.Issues)
	}
}

// FB4b: "Compare the result" is a question after the save: the after chain branches on its Yes and No,
// a port left unconnected means nothing more happens there, and the compile carries the nested cards.
// Before the conversion it is wrong_phase; an end after the save is wrong_phase; the percent tests need
// their percent; the summaries say what each test asks.
func TestCompareTheResultAfterTheSave(t *testing.T) {
	d, by := runDoc()
	save := d.Nodes[len(d.Nodes)-1]
	cmp := node(TypeCheckResult, "Worth it?", map[string]any{"test": "saved_at_least", "percent": float64(30)})
	tagYes := node(TypeTag, "", map[string]any{"add": "big-win"})
	noteNo := node(TypeNote, "", map[string]any{"text": "Only {{saved_percent}} saved"})
	again := node(TypeCheckResult, "", map[string]any{"test": "bigger"})
	tagBig := node(TypeTag, "", map[string]any{"add": "grew"})
	d.Nodes = append(d.Nodes, cmp, tagYes, noteNo, again, tagBig)
	d.Edges = append(d.Edges, link(save.ID, "success", cmp.ID), link(cmp.ID, "match", tagYes.ID), link(cmp.ID, "else", noteNo.ID), link(noteNo.ID, "success", again.ID), link(again.ID, "match", tagBig.ID))
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	for id, want := range map[string]string{cmp.ID: "Only if at least 30% was saved", again.ID: "Only if the new file is bigger"} {
		if r.StepSummaries[id] != want {
			t.Errorf("summary %q, want %q", r.StepSummaries[id], want)
		}
	}
	if a := r.AddSteps[TypeCheckResult]; !a.Addable || a.After != tagYes.ID { // the main path's last after card
		t.Errorf("add after the save: %+v", a)
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	yes := (*tm.Branches)[0]
	if len(yes.After) != 1 || yes.After[0].Check == nil || yes.After[0].Check.Kind != "result" || yes.After[0].Check.Test != "saved_at_least" || yes.After[0].Check.Percent != 30 || yes.After[0].Name != "Worth it?" {
		t.Fatalf("the after question: %+v", yes.After)
	}
	q := yes.After[0]
	if len(q.Yes) != 1 || q.Yes[0].Type != TypeTag || len(q.No) != 2 || q.No[0].Type != TypeNote || q.No[1].Check == nil || q.No[1].Check.Test != "bigger" || len(q.No[1].Yes) != 1 || len(q.No[1].No) != 0 {
		t.Errorf("the nested cards: yes %+v no %+v", q.Yes, q.No)
	}
	if yes.Path[len(yes.Path)-1] != tagBig.ID || !slices.Contains(yes.Path, noteNo.ID) {
		t.Errorf("path %v", yes.Path)
	}
	// the question turned off: its Yes goes on as if it weren't there, and the route leaves it out (review2-582 P3-1)
	cmp.Enabled = false
	d.Nodes[len(d.Nodes)-5] = cmp
	if tm2, _ := ToTemplate(d, rules.Template{}); len((*tm2.Branches)[0].After) != 1 || (*tm2.Branches)[0].After[0].Type != TypeTag || slices.Contains((*tm2.Branches)[0].Path, cmp.ID) || !slices.Contains((*tm2.Branches)[0].Path, tagYes.ID) {
		t.Errorf("off: %+v path %v", (*tm2.Branches)[0].After, (*tm2.Branches)[0].Path)
	}
	tagYes.Enabled = false // an action turned off after the save: out of the route too
	d.Nodes[len(d.Nodes)-4] = tagYes
	if tm3, _ := ToTemplate(d, rules.Template{}); slices.Contains((*tm3.Branches)[0].Path, tagYes.ID) {
		t.Errorf("a disabled after action is on the route: %v", (*tm3.Branches)[0].Path)
	}
	tagYes.Enabled = true
	d.Nodes[len(d.Nodes)-4] = tagYes
	cmp.Enabled = true
	d.Nodes[len(d.Nodes)-5] = cmp
	// the percent tests need their percent; "bigger" doesn't
	cmp.Config = map[string]any{"test": "saved_less_than"}
	d.Nodes[len(d.Nodes)-5] = cmp
	if !hasIssue(Validate(d).Issues, "invalid_config", cmp.ID) {
		t.Error("a percent test without a percent is refused")
	}
	cmp.Config = map[string]any{"test": "saved_at_least", "percent": float64(30)}
	d.Nodes[len(d.Nodes)-5] = cmp
	// an end after the save is refused with its own words
	leave := node(TypeEnd, "", map[string]any{})
	d.Nodes = append(d.Nodes, leave)
	d.Edges = append(d.Edges, link(again.ID, "else", leave.ID))
	res := Validate(d)
	if !hasIssue(res.Issues, "wrong_phase", leave.ID) {
		t.Errorf("an end after the save: %+v", res.Issues)
	}
	d.Nodes = d.Nodes[:len(d.Nodes)-1]
	d.Edges = d.Edges[:len(d.Edges)-1]
	// before the conversion, the question is in the wrong phase (both the edge check and the compile say so)
	d3, by3 := runDoc()
	early := node(TypeCheckResult, "", map[string]any{"test": "bigger"})
	d3.Nodes = append(d3.Nodes, early)
	for i, e := range d3.Edges {
		if e.From == by3["note"].ID {
			d3.Edges[i].To = early.ID
		}
	}
	d3.Edges = append(d3.Edges, link(early.ID, "match", by3["conv"].ID), link(early.ID, "else", by3["fail"].ID))
	if !hasIssue(Validate(d3).Issues, "wrong_phase", early.ID) {
		t.Errorf("before the conversion: %+v", Validate(d3).Issues)
	}
	if _, issues := ToTemplate(d3, rules.Template{}); len(issues) == 0 {
		t.Error("the compile should refuse it too")
	}
	_ = by
}

// review2-579 P3-2: HasFileRefs reads a reference the way the resolver does, spaces and all.
func TestHasFileRefsAgreesWithTheResolver(t *testing.T) {
	for _, v := range []string{"{{file.size}}", " {{file.size}}", "{{ file.size }}", "{{source.height}} "} {
		d := FromTemplate(rules.SliderTemplate(3), nil)
		for i := range d.Nodes {
			if d.Nodes[i].Type == TypeConvert {
				d.Nodes[i].Config["quality"] = v
			}
		}
		if !HasFileRefs(d) {
			t.Errorf("%q is a whole file reference", v)
		}
	}
	for _, v := range []string{"{{q}}", "{{file.size}} and more", "3", "{{file.nothing}}"} {
		d := FromTemplate(rules.SliderTemplate(3), nil)
		for i := range d.Nodes {
			if d.Nodes[i].Type == TypeConvert {
				d.Nodes[i].Config["quality"] = v
			}
		}
		if HasFileRefs(d) {
			t.Errorf("%q is not a whole file reference", v)
		}
	}
}

// FB4b-3: "Compare the result" between 'Check the result' and 'Save the result' compiles into the path's
// before-save tree: the question, the save on its Yes, "Leave the file alone" on its No; a second question
// nests; a port with nothing on it keeps the original; a question turned off is passed over. Anything else
// there, a second save, or the question before the conversion, is refused with its own words.
func TestCompareTheResultBeforeTheSave(t *testing.T) {
	d, by := runDoc()
	var verify, save Node
	for _, n := range d.Nodes {
		switch n.Type {
		case TypeVerify:
			verify = n
		case TypeCommit:
			save = n
		}
	}
	// the chain's verify → save link is replaced by verify → compare → (yes) save, (no) leave
	var edges []Edge
	for _, e := range d.Edges {
		if !(e.From == verify.ID && e.To == save.ID) {
			edges = append(edges, e)
		}
	}
	cmp := node(TypeCheckResult, "Worth it?", map[string]any{"test": "saved_at_least", "percent": float64(30)})
	leave := node(TypeEnd, "", map[string]any{"reason": "Not worth it."})
	d.Nodes = append(d.Nodes, cmp, leave)
	d.Edges = append(edges, link(verify.ID, "success", cmp.ID), link(cmp.ID, "match", save.ID), link(cmp.ID, "else", leave.ID))
	r := Validate(d)
	if r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if r.StepSummaries[cmp.ID] != "Only if at least 30% was saved" {
		t.Errorf("summary %q", r.StepSummaries[cmp.ID])
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
	yes := (*tm.Branches)[0]
	if len(yes.BeforeSave) != 1 || yes.BeforeSave[0].Check == nil || yes.BeforeSave[0].Check.Test != "saved_at_least" || yes.BeforeSave[0].Check.Percent != 30 || yes.BeforeSave[0].Name != "Worth it?" {
		t.Fatalf("the before-save question: %+v", yes.BeforeSave)
	}
	q := yes.BeforeSave[0]
	if len(q.Yes) != 1 || q.Yes[0].Type != TypeCommit || q.Yes[0].NodeID != save.ID || len(q.No) != 1 || q.No[0].Type != TypeEnd || q.No[0].Config["reason"] != "Not worth it." {
		t.Errorf("its leaves: yes %+v no %+v", q.Yes, q.No)
	}
	if i, j := slices.Index(yes.Path, cmp.ID), slices.Index(yes.Path, save.ID); i < 0 || j < 0 || i > j || !slices.Contains(yes.Path, leave.ID) {
		t.Errorf("path %v: the question sits before the save", yes.Path)
	}
	if yes.Then == nil || len(yes.After) != 0 {
		t.Errorf("the conversion compiles as before, with nothing after the save: %+v", yes)
	}
	// a running-time question nested on the No, with nothing on its own No: the original is kept there
	again := node(TypeCheckResult, "", map[string]any{"test": "runtime_within", "seconds": 0.5})
	d.Nodes = append(d.Nodes, again)
	d.Edges = append(d.Edges[:len(d.Edges)-1], link(cmp.ID, "else", again.ID), link(again.ID, "match", save.ID))
	r = Validate(d)
	if r.Errors() {
		t.Fatalf("nested: %+v", r.Issues)
	}
	if r.StepSummaries[again.ID] != "Only if the running time is within 0.5 seconds of the original's" {
		t.Errorf("summary %q", r.StepSummaries[again.ID])
	}
	tm, _ = ToTemplate(d, rules.Template{})
	q = (*tm.Branches)[0].BeforeSave[0]
	if len(q.No) != 1 || q.No[0].Check == nil || q.No[0].Check.Test != "runtime_within" || q.No[0].Check.Seconds != 0.5 || len(q.No[0].Yes) != 1 || q.No[0].Yes[0].Type != TypeCommit || len(q.No[0].No) != 0 {
		t.Errorf("nested: %+v", q.No)
	}
	// the first question turned off: the nested one is what remains
	cmp.Enabled = false
	d.Nodes[len(d.Nodes)-3] = cmp
	if tm2, _ := ToTemplate(d, rules.Template{}); len((*tm2.Branches)[0].BeforeSave) != 1 || (*tm2.Branches)[0].BeforeSave[0].Check == nil || (*tm2.Branches)[0].BeforeSave[0].Check.Test != "saved_at_least" {
		// off: its Yes goes on, which is the save itself
		if bs := (*tm2.Branches)[0].BeforeSave; len(bs) != 1 || bs[0].Type != TypeCommit {
			t.Errorf("off: %+v", bs)
		}
	}
	cmp.Enabled = true
	d.Nodes[len(d.Nodes)-3] = cmp
	// the running-time test needs its seconds
	again.Config = map[string]any{"test": "runtime_within"}
	d.Nodes[len(d.Nodes)-1] = again
	if !hasIssue(Validate(d).Issues, "invalid_config", again.ID) {
		t.Error("a running-time test without its seconds is refused")
	}
	again.Config = map[string]any{"test": "runtime_within", "seconds": 0.5}
	d.Nodes[len(d.Nodes)-1] = again
	// an action between the check and the save is refused, in validation and in the compile
	tag := node(TypeTag, "", map[string]any{"add": "x"})
	d2 := d
	d2.Nodes = append(slices.Clone(d.Nodes), tag)
	d2.Edges = append(slices.Clone(d.Edges), link(again.ID, "else", tag.ID))
	if res := Validate(d2); !hasIssue(res.Issues, "wrong_phase", tag.ID) {
		t.Errorf("a tag between the check and the save: %+v", res.Issues)
	}
	if _, is := ToTemplate(d2, rules.Template{}); !hasIssue(is, "wrong_phase", tag.ID) {
		t.Errorf("the compile: %+v", is)
	}
	// two different saves are refused
	save2 := node(TypeCommit, "", map[string]any{})
	d3 := d
	d3.Nodes = append(slices.Clone(d.Nodes), save2)
	d3.Edges = append(slices.Clone(d.Edges), link(again.ID, "else", save2.ID))
	if _, is := ToTemplate(d3, rules.Template{}); !hasIssue(is, "two_saves", save2.ID) {
		t.Errorf("two saves: %+v", is)
	}
	// the question before the conversion says where it belongs
	early := node(TypeCheckResult, "", map[string]any{"test": "bigger"})
	d4 := d
	d4.Nodes = append(slices.Clone(d.Nodes), early)
	d4.Edges = append(slices.Clone(d.Edges), link(by["note"].ID, "success", early.ID))
	found := ""
	for _, i := range Validate(d4).Issues {
		if i.NodeID == early.ID && i.Code == "wrong_phase" {
			found = i.Message
		}
	}
	if !strings.Contains(found, "belongs after the file is checked") {
		t.Errorf("before the conversion: %q", found)
	}
	// the add menu still puts a new one after the save (moving it up is the steps view's way to the check phase)
	if a := r.AddSteps[TypeCheckResult]; !a.Addable || a.After != save.ID {
		t.Errorf("add: %+v", a)
	}
}
