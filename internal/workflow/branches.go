// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Branches (V6a; slices/v2-engine/v6-plan.md). A workflow with "Check the file" steps compiles to a
// rules.Template whose Branches are its paths: from the library's files through the checks (each with
// the answer the path needs) to either the steps that convert, which run as one ffmpeg process and are
// compiled exactly as a straight workflow is, or "Leave the file alone". Every port of a check must lead
// somewhere (design 02:05Z: every branch has a defined outcome), and no check may sit among the steps
// that convert.

// branchTemplate is d's Template when d has checks (ok false: it isn't a graph of branches at all).
func branchTemplate(d Doc) (rules.Template, []Issue) {
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	out := map[[2]string]string{} // [node, port] -> the next node
	var issues []Issue
	for _, e := range d.Edges {
		k := [2]string{e.From, e.FromPort}
		if out[k] != "" {
			issues = append(issues, Issue{Code: "port_used_twice", Severity: "error", NodeID: e.From, Port: e.FromPort, EdgeID: e.ID,
				Message: "This step's " + portLabel(byID[e.From], e.FromPort) + " leads to two steps. A file can go only one way: remove one of the links."})
			continue
		}
		out[k] = e.To
	}
	var start string
	for _, n := range d.Nodes {
		if n.Type == TypeInput {
			start = n.ID
		}
	}
	if len(issues) > 0 || start == "" {
		return rules.Template{}, issues
	}
	var branches []rules.Branch
	// segment is the path that starts converting at n (after path): one straight run to "Save the
	// result", with what it does when it doesn't work (V6b). nil: an issue was added.
	var segment func(n Node, path []string, fallbacks map[string]bool) *rules.Branch
	segment = func(n Node, path []string, fallbacks map[string]bool) *rules.Branch {
		if IsQuestion(n.Type) || IsEnd(n.Type) || n.Type == TypeInput || IsAction(n.Type) {
			issues = append(issues, Issue{Code: "fallback_must_convert", Severity: "error", NodeID: n.ID,
				Message: "'" + nodeName(n) + "' can't be where a conversion falls back to. Fall back to steps that convert, or leave the file alone."})
			return nil
		}
		if fallbacks[n.ID] {
			issues = append(issues, Issue{Code: "fallback_loop", Severity: "error", NodeID: n.ID,
				Message: "Falling back from '" + nodeName(n) + "' leads back to it. Let the last other path stop or leave the file alone."})
			return nil
		}
		chain := []Node{n}
		var beforeSave []rules.Card // FB4b-3: the questions between the check and the save
		var checkIDs []string       // their node ids, for the path
		for cur := n; ; {
			for _, port := range []string{"skipped", "error"} {
				if out[[2]string{cur.ID, port}] != "" && !(port == "error" && (cur.Type == TypeConvert || cur.Type == TypeVerify)) {
					issues = append(issues, Issue{Code: "not_runnable_yet", Severity: "error", NodeID: cur.ID, Port: port,
						Message: "Steps after '" + portLabel(cur, port) + "' come in a later update. For now, remove that link."})
				}
			}
			next := out[[2]string{cur.ID, "success"}]
			if next == "" {
				break
			}
			nn := byID[next]
			if cur.Type == TypeCommit { // FB2a: after the save come actions on the saved result, nothing else
				break
			}
			if cur.Type == TypeVerify && IsQuestion(nn.Type) && slices.Contains(definitionPhase(nn.Type), PhaseCheck) {
				// FB4b-3: questions about the checked result, before the save: a tree down to the save, an end or nothing
				cards, saveID, ok := checkTree(nn, out, byID, &issues, &checkIDs)
				if !ok {
					return nil
				}
				beforeSave = cards
				if saveID == "" { // no outcome saves: the path ends at the check (path_without_save below)
					break
				}
				nn = byID[saveID]
			}
			if IsQuestion(nn.Type) || IsEnd(nn.Type) || IsAction(nn.Type) || nn.Type == TypeInput { // review2-531 P1: every run card, or the compile recurses
				issues = append(issues, Issue{Code: "branch_in_conversion", Severity: "error", NodeID: nn.ID,
					Message: "'" + nodeName(nn) + "' sits among the steps that run as one conversion. Put checks and actions before the steps that convert."})
				return nil
			}
			chain, cur = append(chain, nn), nn
		}
		// FB2a: the actions after the save, in order; a step that isn't one is wrong_phase (Validate names it too).
		// FB4b: a question after the save ("Compare the result") branches the chain: the cards on its Yes and
		// on its No; a port left unconnected means nothing more happens there.
		var afterIDs []string
		var afterChain func(id string, onPath map[string]bool) ([]rules.Card, bool)
		afterChain = func(id string, onPath map[string]bool) ([]rules.Card, bool) {
			var cards []rules.Card
			for id != "" {
				an, ok := byID[id]
				if !ok || onPath[id] { // a loop: Validate names it (cycle_without_loop)
					return cards, true
				}
				if an.Enabled { // review2-582 P3-1: a step turned off didn't run, so it isn't on the recorded route
					afterIDs = append(afterIDs, id)
				}
				switch {
				case IsQuestion(an.Type) && slices.Contains(definitionPhase(an.Type), PhaseAfter):
					q := questionOf(an)
					if q.Empty() {
						issues = append(issues, Issue{Code: "empty_check", Severity: "error", NodeID: an.ID,
							Message: "'" + q.Name + "' has no comparison, so every file would answer No. Choose one."})
						return nil, false
					}
					seen := clone(onPath)
					seen[id] = true
					yes, ok1 := afterChain(out[[2]string{an.ID, "match"}], seen)
					no, ok2 := afterChain(out[[2]string{an.ID, "else"}], seen)
					if !ok1 || !ok2 {
						return nil, false
					}
					if an.Enabled {
						cards = append(cards, rules.Card{NodeID: an.ID, Type: an.Type, Name: q.Name, Config: clone(an.Config), Check: &q, Yes: yes, No: no})
					} else { // a question turned off is passed over: its Yes goes on
						cards = append(cards, yes...)
					}
					return cards, true
				case IsEnd(an.Type):
					issues = append(issues, Issue{Code: "wrong_phase", Severity: "error", NodeID: id,
						Message: "'" + nodeName(an) + "' can't come after 'Save the result': the file is saved, so nothing ends the run there. Leave that port unconnected instead."})
					return nil, false
				case !IsAction(an.Type) || !slices.Contains(definitionPhase(an.Type), PhaseAfter):
					issues = append(issues, Issue{Code: "wrong_phase", Severity: "error", NodeID: id,
						Message: "'" + nodeName(an) + "' can't follow 'Save the result'. After the save come only steps that act on the saved file."})
					return nil, false
				}
				card := rules.Card{NodeID: an.ID, Type: an.Type, Name: nodeName(an), Config: clone(an.Config)}
				if errNext := out[[2]string{an.ID, "error"}]; errNext != "" { // W9: what runs when this step doesn't work
					errSeen := clone(onPath)
					errSeen[id] = true
					onErr, ok := afterChain(errNext, errSeen)
					if !ok {
						return nil, false
					}
					card.OnError = onErr
				}
				if an.Enabled {
					cards = append(cards, card)
				}
				id = out[[2]string{an.ID, "success"}]
			}
			return cards, true
		}
		after, ok := afterChain(out[[2]string{chain[len(chain)-1].ID, "success"}], map[string]bool{})
		if !ok {
			return nil
		}
		if chain[len(chain)-1].Type != TypeCommit {
			last := chain[len(chain)-1]
			issues = append(issues, Issue{Code: "path_without_save", Severity: "error", NodeID: last.ID, Port: "success",
				Message: "This path never saves the result. End it with 'Save the result', or leave the file alone there."})
			return nil
		}
		// the conversion as a straight workflow; the steps' own choices for failures are the path's
		// (failureOf below), not the straight compile's (it would come back here for ever)
		in := byID[start]
		in.OnError = nil
		plain := []Node{in}
		for _, c := range chain {
			c.OnError = nil
			plain = append(plain, c)
		}
		seg := Doc{SchemaVersion: d.SchemaVersion, Nodes: plain, Variables: d.Variables}
		seg.Edges = chainEdges(seg.Nodes)
		t, is := ToTemplate(seg, rules.Template{})
		if len(is) > 0 {
			issues = append(issues, is...)
			return nil
		}
		ids := []string{}
		for _, c := range chain {
			if c.Type == TypeCommit {
				ids = append(ids, checkIDs...) // the questions sit between the check and the save
			}
			ids = append(ids, c.ID)
		}
		ids = append(ids, afterIDs...)
		b := &rules.Branch{Then: &t, Path: append(clone(path), ids...), BeforeSave: beforeSave, After: after}
		var fs rules.Failures
		for _, c := range chain { // what the run does when it doesn't work (V6b)
			if c.Type != TypeConvert && c.Type != TypeVerify {
				continue
			}
			f := failureOf(c, out, byID, &issues, func(target Node) *rules.Branch {
				seen := clone(fallbacks)
				seen[n.ID] = true
				return segment(target, path, seen)
			})
			if c.Type == TypeConvert {
				fs.Convert = f
			} else {
				fs.Check = f
			}
		}
		if fs.Convert != nil || fs.Check != nil {
			b.OnFailure = &fs
		}
		return b
	}
	// walk follows every path from the input: questions add to checks, actions to before, and each path
	// ends in a conversion (segment), "Leave the file alone" or "Stop as failed".
	var walk func(id string, checks []rules.BranchCheck, before []rules.Card, path []string, onPath map[string]bool)
	walk = func(id string, checks []rules.BranchCheck, before []rules.Card, path []string, onPath map[string]bool) {
		n, ok := byID[id]
		if !ok || onPath[id] { // a cycle: Validate names it (cycle_without_loop)
			return
		}
		switch {
		case IsQuestion(n.Type):
			if !slices.Contains(definitionPhase(n.Type), PhaseBefore) { // FB4b: a question about the result can't be asked before it exists
				issues = append(issues, Issue{Code: "wrong_phase", Severity: "error", NodeID: n.ID,
					Message: "'" + nodeName(n) + "' belongs after the file is saved. Put it after 'Save the result'."})
				return
			}
			c := questionOf(n)
			if c.Empty() {
				issues = append(issues, Issue{Code: "empty_check", Severity: "error", NodeID: n.ID,
					Message: "'" + c.Name + "' has no condition, so every file would answer Yes. Set at least one."})
			}
			for _, port := range []string{"match", "else"} {
				next := out[[2]string{n.ID, port}]
				if next == "" { // design 02:05Z: the UI's quick fix adds "Leave the file alone" on this port
					issues = append(issues, Issue{Code: "unconnected_port", Severity: "error", NodeID: n.ID, Port: port,
						Message: "'" + c.Name + "' has nothing connected to " + portLabel(n, port) + ". Connect a step, or leave the file alone there."})
					continue
				}
				seen := clone(onPath)
				seen[id] = true
				walk(next, append(clone(checks), rules.BranchCheck{NodeID: n.ID, Port: port, Check: c}), before, append(clone(path), n.ID), seen)
			}
		case IsAction(n.Type):
			if !slices.Contains(definitionPhase(n.Type), PhaseBefore) { // FB2a: an action on the saved result can't run before the save
				issues = append(issues, Issue{Code: "wrong_phase", Severity: "error", NodeID: n.ID,
					Message: "'" + nodeName(n) + "' belongs after the file is saved. Put it after 'Save the result'."})
				return
			}
			next := out[[2]string{n.ID, "success"}]
			if next == "" {
				issues = append(issues, Issue{Code: "unconnected_port", Severity: "error", NodeID: n.ID, Port: "success",
					Message: "'" + nodeName(n) + "' has nothing connected to " + portLabel(n, "success") + ". Connect a step, or leave the file alone there."})
				return
			}
			seen := clone(onPath)
			seen[id] = true
			cards := before
			if n.Enabled { // a card turned off is passed over: the path goes on without it
				cards = append(clone(before), rules.Card{NodeID: n.ID, Type: n.Type, Name: nodeName(n), Config: clone(n.Config)})
			}
			walk(next, checks, cards, append(clone(path), n.ID), seen)
		case n.Type == TypeEnd:
			reason, _ := n.Config["reason"].(string)
			if reason == "" {
				reason = "Left alone by this workflow"
			}
			branches = append(branches, rules.Branch{Checks: checks, Before: before, End: reason, Path: append(clone(path), n.ID)})
		case n.Type == TypeFail:
			reason, _ := n.Config["reason"].(string)
			if reason == "" {
				reason = "Stopped by this workflow"
			}
			branches = append(branches, rules.Branch{Checks: checks, Before: before, End: reason, Fail: true, Path: append(clone(path), n.ID)})
		case n.Type == TypeGoto: // FB4b: the file goes on to another workflow; the store fills in its paths (rules.WithHandoffs)
			id, _ := strconv.ParseInt(strOf(n.Config, "workflow", ""), 10, 64)
			branches = append(branches, rules.Branch{Checks: checks, Before: before, Path: append(clone(path), n.ID), Goto: id, GotoNode: n.ID})
		case n.Type == TypeRequeue: // FB4
			reason, _ := n.Config["reason"].(string)
			if reason == "" {
				reason = "Tried as often as this workflow allows"
			}
			branches = append(branches, rules.Branch{Checks: checks, Before: before, End: reason, Fail: true, Path: append(clone(path), n.ID),
				Requeue: &rules.Requeue{AfterSec: intOf(n.Config, "afterMin", 60) * 60, MaxTries: intOf(n.Config, "maxTries", 5)}})
		case n.Type == TypeInput:
			next := out[[2]string{n.ID, "success"}]
			if next == "" {
				issues = append(issues, Issue{Code: "unconnected_port", Severity: "error", NodeID: n.ID, Port: "success",
					Message: "'" + nodeName(n) + "' has nothing connected to " + portLabel(n, "success") + ". Connect a step, or leave the file alone there."})
				return
			}
			seen := clone(onPath)
			seen[id] = true
			walk(next, checks, before, append(clone(path), n.ID), seen)
		default: // the steps that convert, then check and save: one straight run
			if b := segment(n, path, map[string]bool{}); b != nil {
				b.Checks, b.Before = checks, before
				branches = append(branches, *b)
			}
		}
	}
	walk(start, nil, nil, nil, map[string]bool{})
	if len(issues) > 0 {
		return rules.Template{}, issues
	}
	return rules.Template{Kind: rules.KindBranches, CompilerVersion: rules.CompilerVersion, Branches: &branches}, nil
}

// definitionPhase is the phases a step type may sit in ("" for a type the registry lacks).
func definitionPhase(typ string) []string {
	if d, ok := Definition(typ); ok {
		return d.Phase
	}
	return nil
}

// checkTree reads the questions between 'Check the result' and 'Save the result' (FB4b-3), from the first one
// (q), as the cards the worker asks once the result is checked: each question with the cards on its Yes and
// its No, down to a leaf. A leaf is the save (a card of the Save the result step's type: the path goes on),
// "Leave the file alone" or "Stop as failed" (the original is kept, the new file thrown away), or nothing on
// the port (the original is kept too: an outcome with no link keeps the original). Every outcome that saves
// must reach the same Save the result step, since the path compiles as one conversion; a question turned off
// is passed over (its Yes goes on). saveID is that step, "" when no outcome saves; ok is false on an issue.
func checkTree(q Node, out map[[2]string]string, byID map[string]Node, issues *[]Issue, ids *[]string) (cards []rules.Card, saveID string, ok bool) {
	var walk func(id string, onPath map[string]bool) ([]rules.Card, bool)
	walk = func(id string, onPath map[string]bool) ([]rules.Card, bool) {
		if id == "" {
			return nil, true // nothing on the port: the original is kept
		}
		n, found := byID[id]
		if !found || onPath[id] { // a loop: Validate names it (cycle_without_loop)
			return nil, false
		}
		switch {
		case n.Type == TypeCommit:
			if saveID != "" && saveID != id {
				*issues = append(*issues, Issue{Code: "two_saves", Severity: "error", NodeID: id,
					Message: "Every outcome of '" + nodeName(q) + "' that saves must lead to the same 'Save the result' step. Link them to one."})
				return nil, false
			}
			saveID = id
			return []rules.Card{{NodeID: id, Type: TypeCommit, Name: nodeName(n)}}, true
		case n.Type == TypeEnd || n.Type == TypeFail:
			*ids = append(*ids, id)
			reason, _ := n.Config["reason"].(string)
			if reason == "" {
				reason = "Left alone by this workflow"
				if n.Type == TypeFail {
					reason = "Stopped by this workflow"
				}
			}
			return []rules.Card{{NodeID: id, Type: n.Type, Name: nodeName(n), Config: map[string]any{"reason": reason}}}, true
		case IsQuestion(n.Type) && slices.Contains(definitionPhase(n.Type), PhaseCheck):
			*ids = append(*ids, id)
			qn := questionOf(n)
			if qn.Empty() {
				*issues = append(*issues, Issue{Code: "empty_check", Severity: "error", NodeID: id,
					Message: "'" + qn.Name + "' has no comparison, so every file would answer No. Choose one."})
				return nil, false
			}
			seen := clone(onPath)
			seen[id] = true
			yes, ok1 := walk(out[[2]string{id, "match"}], seen)
			no, ok2 := walk(out[[2]string{id, "else"}], seen)
			if !ok1 || !ok2 {
				return nil, false
			}
			if !n.Enabled { // a question turned off is passed over: its Yes goes on
				return yes, true
			}
			return []rules.Card{{NodeID: id, Type: n.Type, Name: qn.Name, Config: clone(n.Config), Check: &qn, Yes: yes, No: no}}, true
		}
		*issues = append(*issues, Issue{Code: "wrong_phase", Severity: "error", NodeID: id,
			Message: "'" + nodeName(n) + "' can't come between 'Check the result' and 'Save the result'. There come only 'Compare the result', the save, 'Leave the file alone' or 'Stop as failed'."})
		return nil, false
	}
	cards, ok = walk(q.ID, map[string]bool{})
	return cards, saveID, ok
}

// questionOf is a question step's config as the question the run asks, with its kind from its type
// and the step's own name for the timeline.
func questionOf(n Node) rules.Question {
	var q rules.Question
	b, _ := json.Marshal(n.Config)
	json.Unmarshal(b, &q) // a value of the wrong kind is an invalid_config from checkConfig already; here it reads as unset
	q.Kind = map[string]string{TypeCheck: "", TypeCheckName: "name", TypeCheckAge: "age", TypeCheckVariable: "variable", TypeCheckLibrary: "library", TypeCheckHardLinks: "hardlinks", TypeCheckTag: "tag", TypeCheckRandom: "random", TypeCheckResult: "result"}[n.Type]
	q.Name = nodeName(n)
	return q
}

// chainEdges link nodes in order, success to in.
func chainEdges(nodes []Node) []Edge {
	var out []Edge
	for i := 1; i < len(nodes); i++ {
		out = append(out, Edge{ID: NewID(), From: nodes[i-1].ID, FromPort: "success", To: nodes[i].ID, ToPort: "in"})
	}
	return out
}

// nodeName is n's own name, or its step's name.
func nodeName(n Node) string {
	if n.Name != "" {
		return n.Name
	}
	if d, ok := Definition(n.Type); ok {
		return d.Name
	}
	return n.Type
}

// portLabel is port's words on n's step ("Yes", "No", "Next").
func portLabel(n Node, port string) string {
	if d, ok := Definition(n.Type); ok {
		for _, p := range d.Outputs {
			if p.Name == port {
				return p.Label
			}
		}
	}
	return port
}

func clone[T any](s T) T {
	b, _ := json.Marshal(s)
	var out T
	json.Unmarshal(b, &out)
	return out
}

// failureOf is node c's failure policy (V6b): its error port's link (another path to convert with, or
// "Leave the file alone"), or its onError; nil: none (today's behaviour).
func failureOf(c Node, out map[[2]string]string, byID map[string]Node, issues *[]Issue, fallback func(Node) *rules.Branch) *rules.Failure {
	add := func(code, msg string) {
		*issues = append(*issues, Issue{Code: code, Severity: "error", NodeID: c.ID, Port: "error", Message: msg})
	}
	f := &rules.Failure{NodeID: c.ID, Name: nodeName(c)}
	target := out[[2]string{c.ID, "error"}]
	if target == "" && c.OnError != nil && c.OnError.Action == "fallback" {
		target = c.OnError.To
	}
	if target != "" {
		if c.OnError != nil && c.OnError.Action != "fallback" {
			add("error_policy_conflict", "'"+f.Name+"' has both a link from "+portLabel(c, "error")+" and its own choice for when it doesn't work. Keep one.")
			return nil
		}
		t, ok := byID[target]
		switch {
		case !ok:
			add("invalid_config", "'"+f.Name+"' falls back to a step that isn't in this workflow.")
			return nil
		case t.Type == TypeEnd:
			f.Action, f.SkipWords = "skip", "Left alone by this workflow"
			if r, _ := t.Config["reason"].(string); r != "" {
				f.SkipWords = r
			}
			return f
		case t.Type == TypeFail:
			f.Action = "stop"
			return f
		case IsQuestion(t.Type) || IsAction(t.Type) || t.Type == TypeInput:
			add("fallback_must_convert", "When '"+f.Name+"' doesn't work, the file can go to other steps that convert, or be left alone; not to a check.")
			return nil
		}
		f.Action, f.Fallback = "fallback", fallback(t)
		if f.Fallback == nil {
			return nil
		}
		return f
	}
	if c.OnError == nil {
		return nil
	}
	e := c.OnError
	switch e.Action {
	case "skip", "stop":
		f.Action = e.Action
		if e.Action == "skip" {
			f.SkipWords = f.Name + " didn't work, so the file was left as it is"
		}
	case "retry":
		f.Action, f.Max, f.BackoffSeconds, f.Then = "retry", e.Max, e.BackoffSeconds, e.Then
		if f.Max == 0 {
			f.Max = 3
		}
		if f.Then == "" {
			f.Then = "stop"
		}
		switch {
		case f.Max < 1 || f.Max > 10:
			add("invalid_config", "Try '"+f.Name+"' 1 to 10 times.")
			return nil
		case f.BackoffSeconds < 0 || f.BackoffSeconds > 86400:
			add("invalid_config", "Wait between tries from 0 seconds to a day.")
			return nil
		case f.Then != "skip" && f.Then != "stop":
			add("invalid_config", "After the last try, stop or leave the file alone.")
			return nil
		}
		f.SkipWords = f.Name + " didn't work after " + strconv.Itoa(f.Max) + " tries, so the file was left as it is"
	default:
		add("invalid_config", "Choose what happens when '"+f.Name+"' doesn't work: try again, leave the file alone, stop, or another path.")
		return nil
	}
	return f
}
