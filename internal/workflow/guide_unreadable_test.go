// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
	"testing"
)

// A workflow with a stretch after the save that the questions can't show still opens: the readable parts
// are answered, the stretch is named in Unreadable, and a save writes the readable parts and keeps the
// stretch byte for byte.
func TestGuideOpensAPartlyUnreadableWorkflow(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.AfterCards = []Card{{Type: TypeCheckResult, Config: map[string]any{"test": "saved_at_least", "percent": 50}, Yes: []Card{{Type: TypeNotify, Config: map[string]any{"kind": "good", "text": "done"}}}}}
	d := Build(a)
	q, n := nodeOf(d, TypeCheckResult), nodeOf(d, TypeNotify)
	d.Edges = append(d.Edges, link(q.ID, "else", n.ID))
	g := GuideOf(d)
	if g.Advanced != CoverageNone || g.Answers == nil || !strings.Contains(g.AdvancedWhy, "its links join at 'Send a notification'") {
		t.Fatalf("%+v", g)
	}
	if !slices.Equal(g.Unreadable, []string{nodeName(q)}) {
		t.Fatalf("unreadable %v, want [%s]", g.Unreadable, nodeName(q))
	}
	got := Apply(d, Answers{Flow: FlowAdvanced, AfterCards: []Card{}, ConvertFails: &Fails{End: "stop", Reason: "Stopped."}})
	if mustJSON(got) == mustJSON(d) {
		t.Fatalf("the readable part was not saved")
	}
	if mustJSON(nodeByIDOf(got, q.ID)) != mustJSON(nodeByIDOf(d, q.ID)) || mustJSON(nodeByIDOf(got, n.ID)) != mustJSON(nodeByIDOf(d, n.ID)) {
		t.Errorf("the unreadable stretch changed")
	}
	if mustJSON(outOf(got, q.ID)) != mustJSON(outOf(d, q.ID)) {
		t.Errorf("the links of the unreadable stretch changed")
	}
}

// A convert step whose error goes to a hand-made step: the questions can't show that link, so it is
// named as unreadable, and a save changes the readable settings but keeps the link.
func TestGuideKeepsAConvertErrorLinkItCantShow(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	conv := nodeOf(d, TypeConvert)
	second := node(TypeConvert, "Second try", map[string]any{"quality": 1, "format": "hevc", "resolution": "keep", "maxBitrate": "none", "speed": "normal"})
	d.Nodes = append(d.Nodes, second)
	d.Edges = append(d.Edges, link(conv.ID, "error", second.ID))
	g := GuideOf(d)
	if g.Advanced != CoverageNone || g.Answers == nil || !slices.Equal(g.Unreadable, []string{nodeName(conv)}) {
		t.Fatalf("%+v", g)
	}
	a := *g.Answers
	a.Flow = FlowAdvanced
	a.Quality = 2
	got := Apply(d, a)
	if intOf(nodeByIDOf(got, conv.ID).Config, "quality", 0) != 2 {
		t.Errorf("the readable quality was not saved")
	}
	if mustJSON(outOf(got, conv.ID)) != mustJSON(outOf(d, conv.ID)) {
		t.Errorf("the unreadable error link changed")
	}
}

// A join in front of the conversion is a hard refusal: no answers, nothing named as unreadable, and a save
// changes nothing.
func TestGuideFrontJoinIsAHardRefusal(t *testing.T) {
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
	if g.Answers != nil || g.Advanced != CoverageNone || len(g.Unreadable) != 0 {
		t.Fatalf("%+v", g)
	}
	if got := Apply(d, Answers{Flow: FlowAdvanced, ConvertFails: &Fails{End: "stop"}}); mustJSON(got) != mustJSON(d) {
		t.Errorf("a save changed a workflow it can't read")
	}
}

// outOf is the links leaving a step, in order.
func outOf(d Doc, id string) []Edge {
	var out []Edge
	for _, e := range d.Edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out
}

// A Basic save keeps a stretch the questions can't show: the notification after Save has a feeder linked into
// it, so the questions can't show it, and a Basic save that turns the notification off leaves the join step and
// its links the same.
func TestGuideBasicSaveKeepsAStretchItCantShow(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.After = After{Notify: true}
	d := Build(a)
	n := nodeOf(d, TypeNotify)
	feeder := node(TypeNotify, "Feeder", map[string]any{"kind": "good", "text": "fed"})
	d.Nodes = append(d.Nodes, feeder)
	d.Edges = append(d.Edges, link(feeder.ID, "", n.ID))
	if g := GuideOf(d); !slices.Equal(g.Unreadable, []string{nodeName(n)}) {
		t.Fatalf("unreadable %v", g.Unreadable)
	}
	basic := DefaultAnswers(GoalSmaller)
	basic.Flow = FlowBasic
	got := Apply(d, basic)
	if nodeByIDOf(got, n.ID).ID == "" {
		t.Fatalf("the join step was deleted by a Basic save")
	}
	if mustJSON(nodeByIDOf(got, n.ID)) != mustJSON(nodeByIDOf(d, n.ID)) {
		t.Errorf("the join step changed on a Basic save")
	}
	if mustJSON(inOf(got, n.ID)) != mustJSON(inOf(d, n.ID)) {
		t.Errorf("the links into the join changed on a Basic save: %s", mustJSON(inOf(got, n.ID)))
	}
}

// inOf is the links arriving at a step, in order.
func inOf(d Doc, id string) []Edge {
	var in []Edge
	for _, e := range d.Edges {
		if e.To == id {
			in = append(in, e)
		}
	}
	return in
}

// The owned card of the after-save stretch is followed by a hand-made check whose two sides join a hand-made step.
// A Basic save the web sends (no before, no after cards) with notify off keeps the card the questions name as
// unreadable.
func TestGuideBasicSaveKeepsAnOwnedCardBeforeAJoin(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.After = After{Notify: true}
	d := Build(a)
	n := nodeOf(d, TypeNotify)
	chk := node(TypeCheckName, "", map[string]any{"pattern": "x"})
	join := node(TypeNotify, "Join", map[string]any{"kind": "good", "text": "joined"})
	d.Nodes = append(d.Nodes, chk, join)
	d.Edges = append(d.Edges, link(n.ID, "success", chk.ID), link(chk.ID, "match", join.ID), link(chk.ID, "else", join.ID))
	if g := GuideOf(d); !slices.Equal(g.Unreadable, []string{nodeName(n)}) {
		t.Fatalf("unreadable %v", g.Unreadable)
	}
	basic := DefaultAnswers(GoalSmaller)
	basic.Flow = FlowBasic
	got := Apply(d, basic)
	if nodeByIDOf(got, n.ID).ID == "" {
		t.Fatalf("the notification the questions can't show was deleted by a Basic save")
	}
	if mustJSON(nodeByIDOf(got, n.ID)) != mustJSON(nodeByIDOf(d, n.ID)) {
		t.Errorf("the notification changed on a Basic save")
	}
}

// A sent error link (the Advanced flow's convertFails) does not replace a convert error link the questions can't
// show: the link and its end step are the same bytes after the save.
func TestGuideSentFailsKeepAnErrorLinkItCantShow(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	conv := nodeOf(d, TypeConvert)
	second := node(TypeConvert, "Second try", map[string]any{"quality": 1, "format": "hevc", "resolution": "keep", "maxBitrate": "none", "speed": "normal"})
	d.Nodes = append(d.Nodes, second)
	d.Edges = append(d.Edges, link(conv.ID, "error", second.ID))
	vfy := nodeOf(d, TypeVerify)
	end := node(TypeEnd, "Done", map[string]any{})
	d.Nodes = append(d.Nodes, end)
	d.Edges = append(d.Edges, link(vfy.ID, "error", end.ID))
	g := GuideOf(d)
	if g.Answers == nil || !slices.Equal(g.Unreadable, []string{nodeName(conv)}) {
		t.Fatalf("%+v", g)
	}
	a := *g.Answers
	a.Flow = FlowAdvanced
	a.ConvertFails = &Fails{End: "stop", Reason: "Stopped."}
	a.CheckFails = &Fails{End: "stop", Reason: "Check stopped."}
	got := Apply(d, a)
	if mustJSON(outOf(got, conv.ID)) != mustJSON(outOf(d, conv.ID)) {
		t.Errorf("the unreadable error link changed: %s", mustJSON(outOf(got, conv.ID)))
	}
	if nodeByIDOf(got, second.ID).ID == "" {
		t.Errorf("the error link's step was deleted")
	}
	// a readable error link elsewhere (the check's) is still written: the skip is for this link only
	if !errLinkTo(got, vfy.ID, TypeFail) {
		t.Errorf("the readable check error link was not written")
	}
}

// errLinkTo says the step's error link leads to a step of the given type.
func errLinkTo(d Doc, id, typ string) bool {
	for _, e := range outOf(d, id) {
		if e.FromPort == "error" && nodeByIDOf(d, e.To).Type == typ {
			return true
		}
	}
	return false
}

// On the owned-card shape, a Basic answer that turns notify off (the read-back has it on) is refused by
// ChangesUnreadable; the same answer with notify on, as read back, is not.
func TestGuideBasicAfterThatChangesAnUnreadableStretchIsRefused(t *testing.T) {
	a := DefaultAnswers(GoalSmaller)
	a.After = After{Notify: true}
	d := Build(a)
	n := nodeOf(d, TypeNotify)
	chk := node(TypeCheckName, "", map[string]any{"pattern": "x"})
	join := node(TypeNotify, "Join", map[string]any{"kind": "good", "text": "joined"})
	d.Nodes = append(d.Nodes, chk, join)
	d.Edges = append(d.Edges, link(n.ID, "success", chk.ID), link(chk.ID, "match", join.ID), link(chk.ID, "else", join.ID))
	basic := DefaultAnswers(GoalSmaller)
	basic.Flow = FlowBasic
	if !ChangesUnreadable(d, basic) {
		t.Errorf("notify off on the owned-card shape: not refused")
	}
	basic.After = After{Notify: true}
	if ChangesUnreadable(d, basic) {
		t.Errorf("notify on, as read back: refused")
	}
	adv := basic
	adv.Flow = FlowAdvanced
	if ChangesUnreadable(d, adv) {
		t.Errorf("an Advanced answer is never refused here")
	}
}

// The after-save stretch starts at a link to a step that isn't in the workflow: the questions can't name that step,
// so the notice names the stretch "the steps after Save", and a Basic answer that turns notify on is refused.
func TestGuideDanglingAfterSaveLinkIsNamedAndRefusesNotify(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	commit := nodeOf(d, TypeCommit)
	d.Edges = append(d.Edges, link(commit.ID, "success", "gone-step"))
	g := GuideOf(d)
	if !slices.Equal(g.Unreadable, []string{"the steps after Save"}) || g.AdvancedWhy == "" {
		t.Fatalf("unreadable %v, advancedWhy %q", g.Unreadable, g.AdvancedWhy)
	}
	basic := DefaultAnswers(GoalSmaller)
	basic.Flow = FlowBasic
	basic.After = After{Notify: true}
	if !ChangesUnreadable(d, basic) {
		t.Errorf("notify on over a dangling after-save link: not refused")
	}
	if got := Apply(d, basic); mustJSON(got) != mustJSON(d) {
		t.Errorf("Apply changed a workflow it refused")
	}
}

// A check's error link the questions can't show (it leads to a notification) is kept as it is when a sent
// CheckFails is applied.
func TestGuideSentCheckFailsKeepAnErrorLinkItCantShow(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	vfy := nodeOf(d, TypeVerify)
	note := node(TypeNotify, "Tell someone", map[string]any{"kind": "bad", "text": "check failed"})
	d.Nodes = append(d.Nodes, note)
	d.Edges = append(d.Edges, link(vfy.ID, "error", note.ID))
	g := GuideOf(d)
	if g.Answers == nil || !slices.Contains(g.Unreadable, nodeName(vfy)) {
		t.Fatalf("%+v", g)
	}
	a := *g.Answers
	a.Flow = FlowAdvanced
	a.CheckFails = &Fails{End: "stop", Reason: "Check stopped."}
	got := Apply(d, a)
	if mustJSON(outOf(got, vfy.ID)) != mustJSON(outOf(d, vfy.ID)) {
		t.Errorf("the unreadable check error link changed: %s", mustJSON(outOf(got, vfy.ID)))
	}
	if nodeByIDOf(got, note.ID).ID == "" {
		t.Errorf("the check's error step was deleted")
	}
}

// Two error links off one convert step: the questions read the first, so an Advanced save with ConvertFails
// would drop the second. The step is named as unreadable, and both error links stay as they are.
func TestGuideTwoErrorLinksOnAConvertAreUnreadable(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	conv := nodeOf(d, TypeConvert)
	end := node(TypeEnd, "Done", map[string]any{})
	other := node(TypeNotify, "Tell me", map[string]any{"kind": "good", "text": "failed"})
	d.Nodes = append(d.Nodes, end, other)
	d.Edges = append(d.Edges, link(conv.ID, "error", end.ID), link(conv.ID, "error", other.ID))
	g := GuideOf(d)
	if g.Answers == nil || !slices.Equal(g.Unreadable, []string{nodeName(conv)}) {
		t.Fatalf("%+v", g)
	}
	a := *g.Answers
	a.Flow = FlowAdvanced
	a.ConvertFails = &Fails{End: "stop", Reason: "Stopped."}
	got := Apply(d, a)
	if mustJSON(outOf(got, conv.ID)) != mustJSON(outOf(d, conv.ID)) {
		t.Errorf("the error links changed: %s", mustJSON(outOf(got, conv.ID)))
	}
}

// A stale notify target with notify off on both sides is not a change: the page keeps the target when the box
// is unchecked, and a save that changes nothing is not refused.
func TestGuideStaleNotifyTargetWithNotifyOffIsNoChange(t *testing.T) {
	d := Build(DefaultAnswers(GoalSmaller))
	commit := nodeOf(d, TypeCommit)
	d.Edges = append(d.Edges, link(commit.ID, "success", "gone-step"))
	basic := DefaultAnswers(GoalSmaller)
	basic.Flow = FlowBasic
	basic.After = After{Notify: false, NotifyTarget: "Phone"}
	if ChangesUnreadable(d, basic) {
		t.Errorf("notify off with a stale target: refused as a change")
	}
}
