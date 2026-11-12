// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
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
		if fallbacks[n.ID] {
			issues = append(issues, Issue{Code: "fallback_loop", Severity: "error", NodeID: n.ID,
				Message: "Falling back from '" + nodeName(n) + "' leads back to it. Let the last other path stop or leave the file alone."})
			return nil
		}
		chain := []Node{n}
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
			if nn.Type == TypeCheck || nn.Type == TypeEnd || nn.Type == TypeInput {
				issues = append(issues, Issue{Code: "branch_in_conversion", Severity: "error", NodeID: nn.ID,
					Message: "'" + nodeName(nn) + "' sits among the steps that run as one conversion. Put checks before the steps that convert."})
				return nil
			}
			chain, cur = append(chain, nn), nn
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
			ids = append(ids, c.ID)
		}
		b := &rules.Branch{Then: &t, Path: append(clone(path), ids...)}
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
	var walk func(id string, checks []rules.BranchCheck, path []string, onPath map[string]bool)
	walk = func(id string, checks []rules.BranchCheck, path []string, onPath map[string]bool) {
		n, ok := byID[id]
		if !ok || onPath[id] { // a cycle: Validate names it (cycle_without_loop)
			return
		}
		switch n.Type {
		case TypeCheck:
			var c rules.VideoCheck
			b, _ := json.Marshal(n.Config)
			json.Unmarshal(b, &c)
			c.Name = nodeName(n)
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
				walk(next, append(clone(checks), rules.BranchCheck{NodeID: n.ID, Port: port, Check: c}), append(clone(path), n.ID), seen)
			}
		case TypeEnd:
			reason, _ := n.Config["reason"].(string)
			if reason == "" {
				reason = "Left alone by this workflow."
			}
			branches = append(branches, rules.Branch{Checks: checks, End: reason, Path: append(clone(path), n.ID)})
		case TypeInput:
			next := out[[2]string{n.ID, "success"}]
			if next == "" {
				issues = append(issues, Issue{Code: "unconnected_port", Severity: "error", NodeID: n.ID, Port: "success",
					Message: "'" + nodeName(n) + "' has nothing connected to " + portLabel(n, "success") + ". Connect a step, or leave the file alone there."})
				return
			}
			seen := clone(onPath)
			seen[id] = true
			walk(next, checks, append(clone(path), n.ID), seen)
		default: // the steps that convert, then check and save: one straight run
			if b := segment(n, path, map[string]bool{}); b != nil {
				b.Checks = checks
				branches = append(branches, *b)
			}
		}
	}
	walk(start, nil, nil, map[string]bool{})
	if len(issues) > 0 {
		return rules.Template{}, issues
	}
	return rules.Template{Kind: rules.KindBranches, CompilerVersion: rules.CompilerVersion, Branches: &branches}, nil
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
			f.Action, f.SkipWords = "skip", "Left alone by this workflow."
			if r, _ := t.Config["reason"].(string); r != "" {
				f.SkipWords = r
			}
			return f
		case t.Type == TypeCheck || t.Type == TypeInput:
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
			f.SkipWords = f.Name + " didn't work, so the file was left as it is."
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
		f.SkipWords = f.Name + " didn't work after " + strconv.Itoa(f.Max) + " tries, so the file was left as it is."
	default:
		add("invalid_config", "Choose what happens when '"+f.Name+"' doesn't work: try again, leave the file alone, stop, or another path.")
		return nil
	}
	return f
}
