// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// FB4b: "Hand to another workflow". A path that hands the file on is replaced by the other workflow's
// paths, each behind this path's checks and actions; the timeline shows the hand-over in its place; a
// target that isn't published, or a loop, ends the run as failed with words that say so.
func TestWithHandoffs(t *testing.T) {
	film := media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, VideoCodec: "h264", Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 3840, Height: 2160}}}
	is4K := BranchCheck{NodeID: "q4k", Port: "match", Check: Question{VideoCheck: VideoCheck{Name: "Is it 4K?", Resolution: 2160}}}
	note := Card{NodeID: "n1", Type: "action.note", Name: "Add a note", Config: map[string]any{"text": "4K"}}
	straight := SliderTemplate(2)
	// B: a workflow with checks of its own: HEVC files are left alone, the rest convert
	isHEVC := BranchCheck{NodeID: "qh", Port: "match", Check: Question{VideoCheck: VideoCheck{Name: "Is it HEVC?", Codecs: []string{"hevc"}}}}
	notHEVC := isHEVC
	notHEVC.Port = "else"
	bThen := SliderTemplate(4)
	b := Template{Kind: KindBranches, Branches: &[]Branch{
		{Checks: []BranchCheck{isHEVC}, End: "Already small", Path: []string{"bin", "qh", "bend"}},
		{Checks: []BranchCheck{notHEVC}, Then: &bThen, Path: []string{"bin", "qh", "bconv"}},
	}}
	// A: 4K files are handed to B (after a note), the rest convert here
	aThen := SliderTemplate(3)
	a := Template{Kind: KindBranches, Branches: &[]Branch{
		{Checks: []BranchCheck{is4K}, Before: []Card{note}, Goto: 2, GotoNode: "g1", Path: []string{"ain", "q4k", "n1", "g1"}},
		{Checks: []BranchCheck{{NodeID: "q4k", Port: "else", Check: is4K.Check}}, Then: &aThen, Path: []string{"ain", "q4k", "aconv"}},
	}}
	flows := map[int64]struct {
		t    Template
		name string
	}{2: {b, "Big films"}, 3: {straight, "Plain"}}
	look := func(id int64) (Template, string, bool) {
		f, ok := flows[id]
		return f.t, f.name, ok
	}

	got := WithHandoffs(a, look)
	if got.Branches == nil || len(*got.Branches) != 3 {
		t.Fatalf("branches: %+v", got.Branches)
	}
	first := (*got.Branches)[0]
	if len(first.Checks) != 2 || first.Checks[0].NodeID != "q4k" || first.Checks[1].NodeID != "qh" || first.Goto != 0 || first.Then != nil || first.End != "Already small" ||
		len(first.Before) != 1 || len(first.Handoffs) != 1 || first.Handoffs[0] != (Handoff{NodeID: "g1", Name: "Big films", AfterChecks: 1}) ||
		strings.Join(first.Path, ",") != "ain,q4k,n1,g1,bin,qh,bend" {
		t.Errorf("the first expanded path: %+v", first)
	}
	if second := (*got.Branches)[1]; second.Then == nil || len(second.Checks) != 2 || second.Checks[1].Port != "else" {
		t.Errorf("the second expanded path: %+v", second)
	}
	if last := (*got.Branches)[2]; last.Then != &aThen && last.Then.Params != aThen.Params || len(last.Handoffs) != 0 {
		t.Errorf("the path that stays: %+v", last)
	}
	// a 4K H.264 film: A's Yes, handed to B, B's No: converted by B's steps; the timeline says so, in order
	plan := got.CompileWith(film, Facts{})
	if plan.Skip != "" || plan.Video.CRF != SliderTemplate(4).Compile(film).Video.CRF {
		t.Fatalf("plan: %+v", plan)
	}
	var words []string
	for _, d := range plan.Decisions {
		words = append(words, d.NodeID+":"+d.Words)
	}
	if len(words) != 3 || words[0] != "q4k:Is it 4K? → Yes (resolution is 2160p)" || words[1] != "g1:Hand to another workflow → Big films" || !strings.HasPrefix(words[2], "qh:Is it HEVC? → No") {
		t.Errorf("decisions: %q", words)
	}
	if len(plan.Before) != 1 || plan.Before[0].NodeID != "n1" {
		t.Errorf("the note before the conversion travels with the file: %+v", plan.Before)
	}
	// a 4K HEVC film: B leaves it alone
	hevc := film
	hevc.VideoCodec, hevc.Streams = "hevc", []media.Stream{{Type: "video", Codec: "hevc", Width: 3840, Height: 2160}}
	if p := got.CompileWith(hevc, Facts{}); p.Skip != "already small" || p.SkipCode != SkipLeftByWorkflow {
		t.Errorf("B's end: %+v", p)
	}

	// a straight target: one path, which converts with its steps
	(*a.Branches)[0].Goto = 3
	if g := WithHandoffs(a, look); len(*g.Branches) != 2 || (*g.Branches)[0].Then == nil || (*g.Branches)[0].Then.Params.Quality != 2 || (*g.Branches)[0].Handoffs[0].Name != "Plain" {
		t.Errorf("a straight target: %+v", (*g.Branches)[0])
	}
	// a target that isn't published: the path fails and says so
	(*a.Branches)[0].Goto = 9
	if p := WithHandoffs(a, look).CompileWith(film, Facts{}); !p.Fail || !strings.Contains(p.Skip, "isn't published any more") {
		t.Errorf("a gone target: %+v", p)
	}
	// a loop: A hands to C, C hands to A
	c := Template{Kind: KindBranches, Branches: &[]Branch{{Goto: 1, GotoNode: "cg", Path: []string{"cin", "cg"}}}}
	flows[1], flows[4] = struct {
		t    Template
		name string
	}{a, "A"}, struct {
		t    Template
		name string
	}{c, "C"}
	(*a.Branches)[0].Goto = 4
	flows[1] = struct {
		t    Template
		name string
	}{a, "A"}
	if p := WithHandoffs(a, look).CompileWith(film, Facts{}); !p.Fail || !strings.Contains(p.Skip, "round in a loop") {
		t.Errorf("a loop: %+v", p)
	}
	// not expanded (a preview): the file is not left alone for good, it goes on elsewhere
	if p := a.CompileWith(film, Facts{}); p.SkipCode != SkipHandedToWorkflow || p.Fail {
		t.Errorf("unexpanded: %+v", p)
	}
	// a straight template and a nil lookup come back as they are
	if g := WithHandoffs(straight, look); g.Branches != nil || g.Params != straight.Params {
		t.Errorf("straight: %+v", g)
	}
	if g := WithHandoffs(a, nil); (*g.Branches)[0].Goto != 4 {
		t.Errorf("nil lookup: %+v", g)
	}
}

// review-579: a template compiled again from its document (a file's typed settings resolved) keeps the
// hand-over paths the store expanded for the run; a hand-over the expanded template has no copy of stays as
// it is. And the hop bound is pinned: a file may be handed on 8 times, not 9.
func TestKeepHandoffsAndTheHopBound(t *testing.T) {
	plain := SliderTemplate(2)
	fresh := Template{Kind: KindBranches, Branches: &[]Branch{
		{Goto: 2, GotoNode: "g1", Path: []string{"in", "g1"}},
		{Goto: 3, GotoNode: "g2", Path: []string{"in", "g2"}},
		{Then: &plain, Path: []string{"in", "conv"}},
	}}
	look := func(id int64) (Template, string, bool) {
		if id == 2 {
			return plain, "Plain", true
		}
		return Template{}, "", false
	}
	expanded := WithHandoffs(fresh, look)
	kept := KeepHandoffs(fresh, expanded)
	if len(*kept.Branches) != 3 || (*kept.Branches)[0].Goto != 0 || (*kept.Branches)[0].Then == nil || (*kept.Branches)[0].Handoffs[0].Name != "Plain" ||
		(*kept.Branches)[1].Goto != 3 || (*kept.Branches)[2].Then == nil {
		t.Errorf("kept: %+v", *kept.Branches)
	}
	if k := KeepHandoffs(fresh, plain); k.Branches != fresh.Branches { // nothing expanded: unchanged
		t.Error("a straight expanded template changes nothing")
	}
	// the hop bound: workflow i hands to i+1; the last one converts
	chain := func(hops int) HandoffLookup {
		return func(id int64) (Template, string, bool) {
			if int(id) > hops {
				return plain, "Last", true
			}
			return Template{Kind: KindBranches, Branches: &[]Branch{{Goto: id + 1, GotoNode: "g", Path: []string{"in", "g"}}}}, "W" + strconvItoa(int(id)), true
		}
	}
	first := Template{Kind: KindBranches, Branches: &[]Branch{{Goto: 1, GotoNode: "g0", Path: []string{"in", "g0"}}}}
	film := media.Info{SizeBytes: 1 << 30, Ext: ".mkv", DurationSec: 100, VideoCodec: "h264", Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}
	// first → W1 → … → W7 → Last: 8 hand-overs in all
	if p := WithHandoffs(first, chain(7)).CompileWith(film, Facts{}); p.Skip != "" || len(p.Decisions) != MaxHandoffs {
		t.Errorf("8 hand-overs should pass: skip %q, %d decisions", p.Skip, len(p.Decisions))
	}
	// one more, and the run stops as failed and says so
	if p := WithHandoffs(first, chain(8)).CompileWith(film, Facts{}); !p.Fail || !strings.Contains(p.Skip, "more than 8 times") {
		t.Errorf("9 hand-overs should fail: %+v", p)
	}
}

func strconvItoa(n int) string { return fmt.Sprint(n) }
