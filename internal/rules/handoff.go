// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

// "Hand to another workflow" (FB4b, docs/architecture/flow-builder.md section 2.7): a path of one workflow
// ends by sending the file on to another published workflow, which then decides as if the file had come
// to it directly. The compile marks the path (Branch.Goto); the store replaces it, when it reads the
// workflow for a run, by the other workflow's paths, each behind this path's checks and actions
// (WithHandoffs). Validation refuses a loop and an unpublished target at publish; a target that goes
// away later, or a loop that appears later, ends the run as failed with words that say so.

// Handoff is one hand-over a path went through: the step that handed the file on, the workflow it went
// to, and how many of the path's checks came before it (the timeline shows it in its place).
type Handoff struct {
	NodeID      string `json:"nodeId"`
	Name        string `json:"name"`
	AfterChecks int    `json:"afterChecks"`
}

// MaxHandoffs is how often a file may be handed from one workflow to another in one run. Beyond it the
// workflows lead round in a loop, or chain further than anyone meant.
const MaxHandoffs = 8

// HandoffLookup is a published workflow by id: its template as published and its name; ok false when
// there is no such workflow, or it was never published.
type HandoffLookup func(id int64) (t Template, name string, ok bool)

// SkipHandedToWorkflow is a path that hands the file to another workflow, compiled without the store's
// lookup (a preview): the file is not left alone for good, it goes on elsewhere.
const SkipHandedToWorkflow = "handed_to_workflow"

// WithHandoffs is t with every path that hands the file to another workflow replaced by that workflow's
// own paths: this path's checks and actions first, then each of theirs, so the file is routed exactly as
// the other workflow would route it, after this one's questions have been answered. A path that hands to
// a workflow that isn't published, or that leads round in a loop, ends the run as failed with words that
// say so, the original untouched. A straight template, or a nil lookup, comes back as it is.
func WithHandoffs(t Template, look HandoffLookup) Template {
	if t.Branches == nil || look == nil {
		return t
	}
	return expandHandoffs(t, look, 0)
}

func expandHandoffs(t Template, look HandoffLookup, depth int) Template {
	if t.Branches == nil {
		return t
	}
	out := make([]Branch, 0, len(*t.Branches))
	for _, b := range *t.Branches {
		if b.Goto == 0 {
			out = append(out, b)
			continue
		}
		if depth >= MaxHandoffs {
			out = append(out, handoffFailed(b, "its workflows hand the file round in a loop, or on more than 8 times"))
			continue
		}
		target, name, ok := look(b.Goto)
		if !ok {
			out = append(out, handoffFailed(b, "the workflow it hands to isn't published any more"))
			continue
		}
		hop := Handoff{NodeID: b.GotoNode, Name: name, AfterChecks: len(b.Checks)}
		prefix := b
		prefix.Goto, prefix.GotoNode = 0, ""
		target = expandHandoffs(target, look, depth+1)
		if target.Branches == nil { // a straight workflow: one path, which converts
			then := target
			nb := prefix
			nb.Then, nb.Handoffs = &then, append(cloneHandoffs(b.Handoffs), hop)
			out = append(out, nb)
			continue
		}
		for _, tb := range *target.Branches {
			nb := tb
			nb.Checks = append(append([]BranchCheck{}, b.Checks...), tb.Checks...)
			nb.Before = append(append([]Card{}, b.Before...), tb.Before...)
			nb.Path = append(append([]string{}, b.Path...), tb.Path...)
			nb.Handoffs = append(append(cloneHandoffs(b.Handoffs), hop), shifted(tb.Handoffs, len(b.Checks))...)
			out = append(out, nb)
		}
	}
	nt := t
	nt.Branches = &out
	return nt
}

// handoffFailed is path b as "Stop as failed" with why, when the hand-over can't happen.
func handoffFailed(b Branch, why string) Branch {
	b.Goto, b.Then, b.Requeue, b.Fail = 0, nil, nil, true
	b.End = "Hand to another workflow: " + why
	return b
}

func cloneHandoffs(h []Handoff) []Handoff { return append([]Handoff{}, h...) }

// shifted are the target's own hand-overs with this path's checks counted before them.
func shifted(h []Handoff, by int) []Handoff {
	out := make([]Handoff, 0, len(h))
	for _, x := range h {
		x.AfterChecks += by
		out = append(out, x)
	}
	return out
}

// handoffDecisions are the path's check answers with each hand-over in its place among them, in the words
// the timeline shows ("Hand to another workflow → Films (4K)").
func handoffDecisions(ds []Decision, hops []Handoff) []Decision {
	if len(hops) == 0 {
		return ds
	}
	out := make([]Decision, 0, len(ds)+len(hops))
	at := 0
	for _, h := range hops {
		for at < len(ds) && at < h.AfterChecks {
			out = append(out, ds[at])
			at++
		}
		out = append(out, Decision{NodeID: h.NodeID, Port: "success", Words: "Hand to another workflow → " + h.Name})
	}
	return append(out, ds[at:]...)
}
