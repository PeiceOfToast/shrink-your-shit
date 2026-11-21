// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The card contract (docs/architecture/flow-builder.md, section 1): every step the registry offers says
// what it is, where it sits, what the file is on each of its outputs, and what it publishes; and this
// build runs every card it offers (a card it can't run is a later slice's, marked Available false).
func TestEveryCardKeepsTheContract(t *testing.T) {
	kinds := []string{RunsInput, RunsQuestion, RunsAction, RunsPlan, RunsVerify, RunsCommit, RunsEnd}
	states := []string{FileOriginal, FileStaged, FileSaved, FileNone}
	for _, d := range Definitions() {
		if !slices.Contains(kinds, d.RunsAs) {
			t.Errorf("%s: runsAs %q is not a kind", d.Type, d.RunsAs)
		}
		if len(d.Phase) == 0 {
			t.Errorf("%s: no phase", d.Type)
		}
		for _, p := range d.Phase {
			if !slices.Contains(Phases, p) {
				t.Errorf("%s: phase %q is not a phase", d.Type, p)
			}
		}
		for _, p := range d.Outputs {
			if st, ok := d.FileOn[p.Name]; !ok || !slices.Contains(states, st) {
				t.Errorf("%s: output %q has no file state (%q)", d.Type, p.Name, st)
			}
		}
		for port := range d.FileOn {
			if !hasPort(d.Outputs, port) {
				t.Errorf("%s: fileOn names %q, which is not an output", d.Type, port)
			}
		}
		if (d.RunsAs == RunsInput) != (len(d.Inputs) == 0) {
			t.Errorf("%s: an input has no in port and every other step has one (%d)", d.Type, len(d.Inputs))
		}
		if (d.RunsAs == RunsEnd) != (len(d.Outputs) == 0) {
			t.Errorf("%s: an end has no outputs and every other step has some (%d)", d.Type, len(d.Outputs))
		}
		for _, v := range d.Publishes {
			if !NamePattern.MatchString(v.Name) || BuiltinName(v.Name) || v.Words == "" {
				t.Errorf("%s: publishes %+v", d.Type, v)
			}
		}
		for _, name := range d.Reads {
			if !NamePattern.MatchString(name) {
				t.Errorf("%s: reads %q", d.Type, name)
			}
		}
		if d.Available == NotYet[d.Type] {
			t.Errorf("%s: available %v, but NotYet says %v", d.Type, d.Available, NotYet[d.Type])
		}
		if d.Name == "" || d.TypeLabel == "" || d.Icon == "" || d.Description == "" {
			t.Errorf("%s: a step with no name, label, icon or description: %+v", d.Type, d)
		}
		if d.Publishes == nil || d.Reads == nil || d.FileOn == nil { // the API sends [] and {}, never null
			t.Errorf("%s: a nil list in the contract", d.Type)
		}
	}
}

// A card this build can't run yet can be saved in a draft, and Publish refuses the workflow naming it (FB1b).
func TestNotYetCardsRefusePublish(t *testing.T) {
	for typ := range NotYet {
		d := Preset()
		n := Node{ID: NewID(), Type: typ, TypeVersion: 1, Enabled: true, Config: map[string]any{"name": "profile", "text": "hello", "reason": "no"}}
		if IsEnd(typ) { // an end hangs off a question's No
			q := Node{ID: NewID(), Type: TypeCheckName, TypeVersion: 1, Enabled: true, Config: map[string]any{"pattern": "x"}}
			first := d.Edges[0]
			d.Nodes = append(d.Nodes, q, n)
			d.Edges = append(d.Edges[1:], Edge{ID: NewID(), From: first.From, FromPort: "success", To: q.ID, ToPort: "in"},
				Edge{ID: NewID(), From: q.ID, FromPort: "match", To: first.To, ToPort: "in"}, Edge{ID: NewID(), From: q.ID, FromPort: "else", To: n.ID, ToPort: "in"})
		} else {
			first := d.Edges[0]
			d.Nodes = append(d.Nodes, n)
			d.Edges = append(d.Edges[1:], Edge{ID: NewID(), From: first.From, FromPort: "success", To: n.ID, ToPort: "in"}, Edge{ID: NewID(), From: n.ID, FromPort: "success", To: first.To, ToPort: "in"})
		}
		r := Validate(d)
		var hit []Issue
		for _, i := range r.Issues {
			if i.Code == "not_runnable_yet" && i.NodeID == n.ID {
				hit = append(hit, i)
			}
		}
		if len(hit) != 1 || !r.Errors() || !strings.Contains(hit[0].Message, "can't run in this version") {
			t.Errorf("%s: %+v", typ, r.Issues)
		}
		if _, issues := ToTemplate(d, rules.Template{}); len(issues) != 0 { // the compile itself is ready for FB1c
			t.Errorf("%s: compile %+v", typ, issues)
		}
	}
}

// The kinds and phases of today's steps, as the design lists them (section 2).
func TestTodaysCardsKindsAndPhases(t *testing.T) {
	want := map[string][2]string{
		TypeInput: {RunsInput, PhaseBefore}, TypeSkips: {RunsPlan, PhaseConvert}, TypeConvert: {RunsPlan, PhaseConvert},
		TypeAudio: {RunsPlan, PhaseConvert}, TypeSubtitles: {RunsPlan, PhaseConvert}, TypeContainer: {RunsPlan, PhaseConvert},
		TypeVerify: {RunsVerify, PhaseCheck}, TypeCommit: {RunsCommit, PhaseSave}, TypeLegacy: {RunsPlan, PhaseConvert},
		TypeCheck: {RunsQuestion, PhaseBefore}, TypeEnd: {RunsEnd, PhaseBefore},
	}
	for typ, w := range want {
		d, ok := Definition(typ)
		if !ok || d.RunsAs != w[0] || !slices.Contains(d.Phase, w[1]) {
			t.Errorf("%s: %s %v, want %v", typ, d.RunsAs, d.Phase, w)
		}
	}
	// the file's state: a conversion's Next carries the new file, a save's carries it saved, the rest the original
	conv, _ := Definition(TypeConvert)
	save, _ := Definition(TypeCommit)
	check, _ := Definition(TypeCheck)
	if conv.FileOn["success"] != FileStaged || conv.FileOn["error"] != FileOriginal || conv.FileOn["skipped"] != FileOriginal ||
		save.FileOn["success"] != FileSaved || save.FileOn["error"] != FileOriginal || check.FileOn["match"] != FileOriginal || check.FileOn["else"] != FileOriginal {
		t.Errorf("file states: convert %v save %v check %v", conv.FileOn, save.FileOn, check.FileOn)
	}
}
