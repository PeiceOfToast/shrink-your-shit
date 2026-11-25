// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Advanced lists round-trip: conditions in front with what they turn away (an end, actions then an
// end, on either side), actions in front, cards after the save with a question's two sides and what
// runs when a card doesn't work, what the conversion does when it doesn't work, and the variables. The
// built workflow validates and compiles.
func TestGuideAdvancedListsRoundTrip(t *testing.T) {
	allFormats(t)
	a := DefaultAnswers(GoalSmaller)
	a.Before = []Card{
		{Type: TypeCheckName, Config: map[string]any{"pattern": "sample", "match": "contains"}, Then: &Then{Side: "yes", End: "leave", Config: map[string]any{"reason": "A sample."}}},
		{Type: TypeNote, Config: map[string]any{"text": "starting"}},
		{Type: TypeCheck, Config: map[string]any{"resolution": 2160}, Then: &Then{Actions: []Card{{Type: TypeTag, Config: map[string]any{"add": "small"}}}, End: "later", Config: map[string]any{"afterMin": 30, "maxTries": 2}}},
		{Type: TypeCheckTag, Config: map[string]any{"tags": "hold", "tagOp": "any"}, Then: &Then{Side: "yes", End: "stop", Config: map[string]any{"reason": "On hold."}}},
	}
	a.AfterCards = []Card{
		{Type: TypeCopy, Config: map[string]any{"folder": t.TempDir()}, OnError: []Card{{Type: TypeNote, Config: map[string]any{"text": "no copy"}}}},
		{Type: TypeCheckResult, Config: map[string]any{"test": "saved_at_least", "percent": 50},
			Yes: []Card{{Type: TypeMediaServer, Config: map[string]any{"which": "all"}}},
			No:  []Card{{Type: TypeNotify, Config: map[string]any{"kind": "warning", "text": "barely smaller"}}}},
	}
	a.ConvertFails = &Fails{End: "leave", Reason: "Couldn't convert."}
	a.CheckFails = &Fails{End: "retry", Max: 2, BackoffSeconds: 60, Then: "stop"}
	a.Variables = []Variable{{Name: "profile", Type: "text", Value: "tv", HasValue: true}}
	if iss := Check(a); len(iss) > 0 {
		t.Fatalf("%+v", iss)
	}
	d := Build(a)
	if r := Validate(d); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if _, iss := ToTemplate(d, rules.Template{}); len(iss) > 0 {
		t.Fatalf("compile: %+v", iss)
	}
	g := GuideOf(d)
	if g.Advanced != CoverageFull || g.Coverage != CoverageNone || !strings.Contains(g.Why, "'Check the file name' sits where the questions can't show it") {
		t.Fatalf("%s / %s: %s %s", g.Coverage, g.Advanced, g.Why, g.AdvancedWhy)
	}
	b := g.Answers
	if len(b.Before) != 4 || b.Before[0].Then.Side != "yes" || b.Before[0].Then.End != "leave" || b.Before[0].Then.Config["reason"] != "A sample." ||
		b.Before[1].Type != TypeNote || len(b.Before[2].Then.Actions) != 1 || b.Before[2].Then.End != "later" || intOf(b.Before[2].Then.Config, "afterMin", 0) != 30 ||
		b.Before[3].Then.End != "stop" || b.Before[3].Then.Side != "yes" {
		t.Errorf("before: %s", mustJSON(b.Before))
	}
	if len(b.AfterCards) != 2 || len(b.AfterCards[0].OnError) != 1 || b.AfterCards[0].OnError[0].Type != TypeNote ||
		len(b.AfterCards[1].Yes) != 1 || b.AfterCards[1].Yes[0].Type != TypeMediaServer || len(b.AfterCards[1].No) != 1 || b.AfterCards[1].No[0].Config["kind"] != "warning" {
		t.Errorf("after: %s", mustJSON(b.AfterCards))
	}
	if b.ConvertFails == nil || b.ConvertFails.End != "leave" || b.ConvertFails.Reason != "Couldn't convert." ||
		b.CheckFails == nil || b.CheckFails.End != "retry" || b.CheckFails.Max != 2 || b.CheckFails.BackoffSeconds != 60 || b.CheckFails.Then != "stop" {
		t.Errorf("fails: %+v %+v", b.ConvertFails, b.CheckFails)
	}
	if len(b.Variables) != 1 || b.Variables[0].Name != "profile" {
		t.Errorf("variables: %+v", b.Variables)
	}
	// Basic's views of the same lists: the owned pair isn't first, so no Skip; the after cards aren't its run
	if b.Skip != (Skip{}) || b.After != (After{}) {
		t.Errorf("basic views: %+v %+v", b.Skip, b.After)
	}
	// applied again unchanged: the same workflow, same ids
	d2 := Apply(d, *b)
	if mustJSON(GuideOf(d2).Answers) != mustJSON(b) {
		t.Errorf("not stable:\n%s\n%s", mustJSON(GuideOf(d2).Answers), mustJSON(b))
	}
	if len(d2.Nodes) != len(d.Nodes) {
		t.Errorf("%d nodes, had %d", len(d2.Nodes), len(d.Nodes))
	}
	// changed: the first condition gone, the question after the save without its No, converting may fall back to stop
	c := *b
	c.Before = c.Before[1:]
	c.AfterCards[1].No = nil
	c.ConvertFails = &Fails{End: "stop"}
	c.CheckFails = &Fails{End: "none"}
	d3 := Apply(d, c)
	if r := Validate(d3); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	g3 := GuideOf(d3)
	if len(g3.Answers.Before) != 3 || g3.Answers.Before[0].Type != TypeNote || len(g3.Answers.AfterCards[1].No) != 0 ||
		g3.Answers.ConvertFails.End != "stop" || g3.Answers.CheckFails.End != "none" {
		t.Errorf("changed: %s", mustJSON(g3.Answers))
	}
	if got := Steps(d3); !strings.HasPrefix(strings.Join(got, " > "), "Library files > Add a note to the log > Check the file > Has a tag > Leave some files alone") {
		t.Errorf("steps: %v", got)
	}
	// empty lists take everything away; the Basic views then read empty too
	e := *b
	e.Before, e.AfterCards, e.Variables = []Card{}, []Card{}, []Variable{}
	g4 := GuideOf(Apply(d, e))
	if g4.Coverage != CoverageFull || len(g4.Answers.Before) != 0 || len(g4.Answers.AfterCards) != 0 || len(g4.Answers.Variables) != 0 {
		t.Errorf("emptied: %s %s", g4.Coverage, mustJSON(g4.Answers))
	}
}

// Basic's Skip and After are views of the lists: a Basic save keeps the lists it doesn't show, and the
// lists show what a Basic save made.
func TestGuideBasicViewsAreTheLists(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.Skip, a.After = Skip{MinSizeGB: 2}, After{Downloader: true}
	g := GuideOf(Build(a))
	if g.Coverage != CoverageFull || g.Advanced != CoverageFull || len(g.Answers.Before) != 1 || g.Answers.Before[0].Type != TypeCheck ||
		g.Answers.Before[0].Then.End != "leave" || len(g.Answers.AfterCards) != 1 || g.Answers.AfterCards[0].Type != TypeDownloader {
		t.Fatalf("%s", mustJSON(g))
	}
	// a Basic save with the lists left unanswered (nil) keeps an Advanced-made stretch
	b := *g.Answers
	b.Before = append(b.Before, Card{Type: TypeNote, Config: map[string]any{"text": "hi"}})
	d := Apply(Build(a), b)
	basic := Answers{Goal: GoalSmaller, Skip: Skip{MinSizeGB: 3}, After: After{Downloader: true, MediaServer: true}}
	g2 := GuideOf(Apply(d, basic))
	if g2.Coverage != CoveragePartial || len(g2.Answers.Before) != 2 || g2.Answers.Skip.MinSizeGB != 3 || len(g2.Answers.AfterCards) != 2 {
		t.Errorf("%s", mustJSON(g2))
	}
}

// What the questions can't show: a condition whose both sides go on, a conversion that falls back to
// another conversion. Both flows say so, and Apply changes nothing.
func TestGuideAdvancedRefusals(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	skips := nodeOf(d, TypeSkips)
	q := node(TypeCheckName, "", map[string]any{"pattern": "x"})
	d.Nodes = append(d.Nodes, q)
	for i := range d.Edges {
		if d.Edges[i].To == skips.ID {
			d.Edges[i].To = q.ID
		}
	}
	d.Edges = append(d.Edges, link(q.ID, "match", skips.ID), link(q.ID, "else", skips.ID))
	g := GuideOf(d)
	if g.Advanced != CoverageNone || g.Answers != nil || !strings.Contains(g.AdvancedWhy, "both sides of 'Check the file name' go on") {
		t.Errorf("%+v", g)
	}
	if got := Apply(d, Answers{}); mustJSON(got) != mustJSON(d) {
		t.Errorf("changed a workflow it can't read")
	}
	d2 := Build(DefaultAnswers(GoalSmaller))
	conv, verify := nodeOf(d2, TypeConvert), nodeOf(d2, TypeVerify)
	_ = verify
	second := node(TypeConvert, "Second try", map[string]any{"quality": 1, "format": "hevc", "resolution": "keep", "maxBitrate": "none", "speed": "normal"})
	d2.Nodes = append(d2.Nodes, second)
	d2.Edges = append(d2.Edges, link(conv.ID, "error", second.ID))
	if g := GuideOf(d2); g.Advanced != CoverageNone || !strings.Contains(g.AdvancedWhy, "goes to other steps that convert") {
		t.Errorf("%+v", g)
	}
	// a wrong card is refused with its place in the list
	iss := Check(Answers{Before: []Card{{Type: TypeCheckName, Config: map[string]any{"pattern": "x"}}, {Type: TypeMediaServer}},
		AfterCards: []Card{{Type: TypeCheckName}, {Type: TypeTag, OnError: []Card{{Type: TypeNote}}}}, ConvertFails: &Fails{End: "retry", Max: 20}})
	var fields []string
	for _, i := range iss {
		fields = append(fields, i.Field)
	}
	if strings.Join(fields, ",") != "afterCards.0,afterCards.1.onError,before.0.then,before.1,convertFails" {
		t.Errorf("%v", fields)
	}
}

// The summary says what each card does.
func TestGuideAdvancedSummary(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.Before = []Card{{Type: TypeCheckName, Config: map[string]any{"pattern": "sample", "match": "contains"}, Then: &Then{Side: "yes", End: "leave"}}}
	a.AfterCards = []Card{{Type: TypeChecksum, Config: map[string]any{"kind": "sha256"}, OnError: []Card{{Type: TypeNote, Config: map[string]any{"text": "x"}}}}}
	a.ConvertFails = &Fails{End: "stop"}
	got := strings.Join(Summary(a), "\n")
	for _, s := range []string{"Check the file name: ", "the files that match are left alone.", "If converting doesn't work, the job stops as failed.", "Afterwards, checksum the result", "if it doesn't work, add a note to the log."} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in\n%s", s, got)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
