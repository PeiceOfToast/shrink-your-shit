// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"slices"
	"strings"
)

// The Advanced flow's lists (slice 12, G1b): every condition and action in front of the conversion, every
// card after the save, and what the conversion does when a step doesn't work. A list is a tree: a
// condition in front turns files away on one side (Then) and goes on with the other; a question after the
// save has cards on its Yes and its No; a card after the save has what runs when it doesn't work. The
// Basic flow's Skip and After are views of the same lists (GuideOf fills both; Apply takes the lists when
// they are given, else the views).

// Card is one step the Advanced flow asks about, with what hangs off it.
type Card struct {
	ID      string         `json:"id,omitempty"` // kept across saves when it is still there; empty for a new card
	Type    string         `json:"type"`
	Name    string         `json:"name,omitempty"`
	Config  map[string]any `json:"config"`
	Off     bool           `json:"off,omitempty"`  // the step is turned off
	Then    *Then          `json:"then,omitempty"` // a condition in front: the files it turns away
	Yes     []Card         `json:"yes,omitempty"`  // a question after the save: its two sides
	No      []Card         `json:"no,omitempty"`
	OnError []Card         `json:"onError,omitempty"` // after the save: what runs when this card doesn't work
}

// Then is what happens to the files a condition turns away: actions, then an end.
type Then struct {
	Side    string         `json:"side,omitempty"` // "no" (default): the files that don't match are turned away; "yes": the ones that match
	Actions []Card         `json:"actions,omitempty"`
	End     string         `json:"end"`              // leave | stop | later | handoff
	Config  map[string]any `json:"config,omitempty"` // the end's settings: reason; afterMin, maxTries; workflow
}

// Fails is what the conversion does when Convert video or Check the result doesn't work (A12). Nil: the
// step's own choice stays as it is.
type Fails struct {
	End            string `json:"end"` // none | leave | stop | retry
	Reason         string `json:"reason,omitempty"`
	Max            int    `json:"max,omitempty"`            // retry
	BackoffSeconds int    `json:"backoffSeconds,omitempty"` // retry
	Then           string `json:"then,omitempty"`           // retry: skip | stop
}

// The ends a Then may have, and the step each one is.
var thenEnds = map[string]string{"leave": TypeEnd, "stop": TypeFail, "later": TypeRequeue, "handoff": TypeGoto}

// links is a workflow's edges by node and port, for reading.
type links struct {
	byID map[string]Node
	out  map[[2]string]string // [node, port] -> the next node (the first link, when a port has several)
	fan  map[[2]string]int    // [node, port] -> how many links leave it
	in   map[string]int       // node -> how many links lead into it
}

func linksOf(d Doc) links {
	l := links{byID: map[string]Node{}, out: map[[2]string]string{}, fan: map[[2]string]int{}, in: map[string]int{}}
	for _, n := range d.Nodes {
		l.byID[n.ID] = n
	}
	for _, e := range d.Edges {
		l.fan[[2]string{e.From, e.FromPort}]++
		l.in[e.To]++ // "how many links lead into it": every link, whichever port it leaves; a second link off one port is stray's job (fan)
		if _, dup := l.out[[2]string{e.From, e.FromPort}]; dup {
			continue
		}
		l.out[[2]string{e.From, e.FromPort}] = e.To
	}
	return l
}

// stray says a node has a link off a port the questions don't read (review-716 P1), or more than one
// link off a port they do read (review2-716 P3): hand-made, so the questions leave the workflow alone.
func (l links) stray(id string, ports ...string) bool {
	for k, n := range l.fan {
		if k[0] == id && (!slices.Contains(ports, k[1]) || n > 1) {
			return true
		}
	}
	return false
}

// joined says more than one link leads into a node (review-716 P2): a tree can't show it, and writing
// it from two branches would give two steps one id.
func (l links) joined(id string) bool { return l.in[id] != 1 }

func (l links) next(id, port string) (Node, bool) {
	n, ok := l.byID[l.out[[2]string{id, port}]]
	return n, ok
}

func cardOf(n Node) Card {
	return Card{ID: n.ID, Type: n.Type, Name: n.Name, Config: clone(n.Config), Off: !n.Enabled}
}

// readBefore reads the conditions and actions between Library files and the first step that converts:
// each condition turns files away on one side (actions, then an end) and goes on with the other. It
// returns the cards and the first converting step's id. why says what stops it.
func readBefore(l links, input string) (cards []Card, first string, why string) {
	seen := map[string]bool{}
	cur, ok := l.next(input, "success")
	if !ok {
		return nil, "", "'Library files' leads nowhere"
	}
	for {
		if seen[cur.ID] {
			return nil, "", "its links go round in a circle"
		}
		seen[cur.ID] = true
		if !slices.Contains(PresetOrder, cur.Type) && l.joined(cur.ID) {
			return nil, "", "its links join at '" + nodeName(cur) + "'"
		}
		switch {
		case slices.Contains(PresetOrder, cur.Type):
			return cards, cur.ID, ""
		case IsQuestion(cur.Type):
			if l.stray(cur.ID, "match", "else") {
				return nil, "", "'" + nodeName(cur) + "' has a link the questions don't show"
			}
			card := cardOf(cur)
			yes, yesOK := l.next(cur.ID, "match")
			no, noOK := l.next(cur.ID, "else")
			if !yesOK || !noOK {
				return nil, "", "'" + nodeName(cur) + "' has a side with nothing connected"
			}
			yesThen, yesOn, yesWhy := readThen(l, yes)
			noThen, noOn, noWhy := readThen(l, no)
			switch {
			case yesWhy != "":
				return nil, "", yesWhy
			case noWhy != "":
				return nil, "", noWhy
			case !yesOn && !noOn:
				return nil, "", "both sides of '" + nodeName(cur) + "' end, so nothing converts"
			case yesOn && noOn:
				return nil, "", "both sides of '" + nodeName(cur) + "' go on; a question can only turn files away on one side"
			case !noOn:
				noThen.Side = "no"
				card.Then, cur = noThen, yes
			default:
				yesThen.Side = "yes"
				card.Then, cur = yesThen, no
			}
			cards = append(cards, card)
		case IsAction(cur.Type):
			if l.stray(cur.ID, "success") {
				return nil, "", "'" + nodeName(cur) + "' has a link the questions don't show"
			}
			cards = append(cards, cardOf(cur))
			n, ok := l.next(cur.ID, "success")
			if !ok {
				return nil, "", "'" + nodeName(cur) + "' leads nowhere"
			}
			cur = n
		case IsEnd(cur.Type):
			return nil, "", "the files all end at '" + nodeName(cur) + "' before anything converts"
		default:
			return nil, "", "'" + nodeName(cur) + "' sits before the steps that convert"
		}
	}
}

// readThen reads one side of a condition as actions then an end. goesOn: the side goes on to the
// conversion instead (so it isn't a Then). why: the questions can't read it at all.
func readThen(l links, n Node) (t *Then, goesOn bool, why string) {
	t = &Then{}
	seen := map[string]bool{}
	for {
		if seen[n.ID] {
			return nil, false, "its links go round in a circle"
		}
		seen[n.ID] = true
		switch {
		case IsEnd(n.Type):
			if l.joined(n.ID) { // one end shared by two conditions: a tree can't show it
				return nil, false, "its links join at '" + nodeName(n) + "'"
			}
			if l.stray(n.ID) {
				return nil, false, "'" + nodeName(n) + "' has a link the questions don't show"
			}
			for k, typ := range thenEnds {
				if typ == n.Type {
					t.End = k
				}
			}
			t.Config = clone(n.Config)
			if len(t.Config) == 0 {
				t.Config = nil
			}
			return t, false, ""
		case IsAction(n.Type) && slices.Contains(definitionPhase(n.Type), PhaseBefore):
			if l.joined(n.ID) {
				return nil, false, "its links join at '" + nodeName(n) + "'"
			}
			if l.stray(n.ID, "success") {
				return nil, false, "'" + nodeName(n) + "' has a link the questions don't show"
			}
			t.Actions = append(t.Actions, cardOf(n))
			next, ok := l.next(n.ID, "success")
			if !ok {
				return nil, false, "'" + nodeName(n) + "' leads nowhere"
			}
			n = next
		default:
			return nil, true, ""
		}
	}
}

// readAfter reads the cards after the save from id on: actions with what runs when they don't work, and
// questions with their two sides. A port with nothing on it is an empty list.
func readAfter(l links, id string, onPath map[string]bool) ([]Card, string) {
	cards := []Card{}
	for id != "" {
		n, ok := l.byID[id]
		if !ok {
			return nil, "a link leads to a step that isn't there"
		}
		if onPath[id] {
			return nil, "its links go round in a circle"
		}
		onPath = clone(onPath)
		onPath[id] = true
		if l.joined(id) {
			return nil, "its links join at '" + nodeName(n) + "'"
		}
		card := cardOf(n)
		switch {
		case IsQuestion(n.Type):
			if l.stray(id, "match", "else") {
				return nil, "'" + nodeName(n) + "' has a link the questions don't show"
			}
			var why string
			if card.Yes, why = readAfter(l, l.out[[2]string{n.ID, "match"}], onPath); why != "" {
				return nil, why
			}
			if card.No, why = readAfter(l, l.out[[2]string{n.ID, "else"}], onPath); why != "" {
				return nil, why
			}
			return append(cards, card), ""
		case IsAction(n.Type):
			if l.stray(id, "success", "error") {
				return nil, "'" + nodeName(n) + "' has a link the questions don't show"
			}
			if e := l.out[[2]string{n.ID, "error"}]; e != "" {
				var why string
				if card.OnError, why = readAfter(l, e, onPath); why != "" {
					return nil, why
				}
			}
			cards = append(cards, card)
			id = l.out[[2]string{n.ID, "success"}]
		default:
			return nil, "'" + nodeName(n) + "' sits after the save"
		}
	}
	return cards, ""
}

// readFails reads what a converting step does when it doesn't work: its error link to an end, or its
// own choice. why: it falls back to another conversion, which the questions can't show.
func readFails(l links, n Node) (*Fails, string) {
	if l.fan[[2]string{n.ID, "error"}] > 1 { // a second error link is hand-made: the questions read only the first, so the rest would be lost
		return nil, "when '" + nodeName(n) + "' doesn't work the file goes to other steps that convert"
	}
	if t, ok := l.next(n.ID, "error"); ok {
		switch t.Type {
		case TypeEnd:
			return &Fails{End: "leave", Reason: strOf(t.Config, "reason", "")}, ""
		case TypeFail:
			return &Fails{End: "stop", Reason: strOf(t.Config, "reason", "")}, ""
		}
		return nil, "when '" + nodeName(n) + "' doesn't work the file goes to other steps that convert"
	}
	if n.OnError == nil {
		return &Fails{End: "none"}, ""
	}
	switch n.OnError.Action {
	case "skip":
		return &Fails{End: "leave"}, ""
	case "stop":
		return &Fails{End: "stop"}, ""
	case "retry":
		return &Fails{End: "retry", Max: n.OnError.Max, BackoffSeconds: n.OnError.BackoffSeconds, Then: n.OnError.Then}, ""
	}
	return nil, "when '" + nodeName(n) + "' doesn't work the file goes to other steps that convert"
}

// subtree is every node reachable from id over any port, for taking a stretch out.
func subtree(d Doc, id string, stop map[string]bool) map[string]bool {
	out := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if id == "" || out[id] || stop[id] {
			return
		}
		out[id] = true
		for _, e := range d.Edges {
			if e.From == id {
				visit(e.To)
			}
		}
	}
	visit(id)
	return out
}

// keptLinks are a stretch's links before it is rebuilt, by from, port and to, so a link the rebuild makes
// again keeps its id and its label (a label is a hand edit; QA-716). An end node an unchanged link
// led to is kept too, so re-applying unchanged answers changes no bytes.
type keptLinks struct {
	edges map[[3]string]Edge
	byOut map[[2]string]Node // the node a port led to
	used  map[string]bool
}

func keep(d Doc) keptLinks {
	k := keptLinks{edges: map[[3]string]Edge{}, byOut: map[[2]string]Node{}, used: map[string]bool{}}
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	for _, e := range d.Edges {
		k.edges[[3]string{e.From, e.FromPort, e.To}] = e
		if n, ok := byID[e.To]; ok {
			k.byOut[[2]string{e.From, e.FromPort}] = n
		}
	}
	return k
}

// link is the edge from a port to a node: the old one when the same link was there, else new.
func (k keptLinks) link(from, port, to string) Edge {
	if e, ok := k.edges[[3]string{from, port, to}]; ok && !k.used[e.ID] {
		k.used[e.ID] = true
		return e
	}
	return Edge{ID: NewID(), From: from, FromPort: port, To: to, ToPort: "in"}
}

// end is the end node for a port: the one the port led to, when it is of the same kind and not used
// elsewhere (its settings come from the answer), else new.
func (k keptLinks) end(from, port, typ string, config map[string]any) Node {
	n := Node{ID: NewID(), Type: typ, TypeVersion: 1, Enabled: true, Config: config}
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	if old, ok := k.byOut[[2]string{from, port}]; ok && old.Type == typ && !k.used[old.ID] {
		k.used[old.ID] = true
		n.ID, n.TypeVersion = old.ID, old.TypeVersion
	}
	return n
}

// nodeFor is the node a card becomes: its old node when it is still there (keeping its id, and the
// settings the card doesn't carry), else a new one.
func nodeFor(c Card, byID map[string]Node) Node {
	n := Node{ID: c.ID, Type: c.Type, TypeVersion: 1, Name: c.Name, Enabled: !c.Off, Config: clone(c.Config)}
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	if old, ok := byID[c.ID]; ok && old.Type == c.Type {
		n.TypeVersion, n.OnError = old.TypeVersion, old.OnError
	} else {
		n.ID = NewID()
	}
	return n
}

// applyBefore rebuilds everything between Library files and the first converting step from cards.
func applyBefore(d Doc, w walked, cards []Card) Doc {
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	first := w.first
	kept := keep(d)
	gone := subtree(d, d.Edges[slices.IndexFunc(d.Edges, func(e Edge) bool { return e.From == w.by[TypeInput] && e.FromPort == "success" })].To,
		map[string]bool{first: true})
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return gone[n.ID] })
	d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool {
		return gone[e.From] || gone[e.To] || (e.From == w.by[TypeInput] && e.FromPort == "success")
	})
	prev, prevPort := w.by[TypeInput], "success"
	link := func(from, port, to string) { d.Edges = append(d.Edges, kept.link(from, port, to)) }
	for _, c := range cards {
		n := nodeFor(c, byID)
		d.Nodes = append(d.Nodes, n)
		link(prev, prevPort, n.ID)
		if IsQuestion(c.Type) && c.Then != nil {
			away, on := "else", "match"
			if c.Then.Side == "yes" {
				away, on = "match", "else"
			}
			p, pp := n.ID, away
			for _, ac := range c.Then.Actions {
				an := nodeFor(ac, byID)
				d.Nodes = append(d.Nodes, an)
				link(p, pp, an.ID)
				p, pp = an.ID, "success"
			}
			endType := thenEnds[c.Then.End]
			if endType == "" {
				endType = TypeEnd
			}
			end := kept.end(p, pp, endType, clone(c.Then.Config))
			d.Nodes = append(d.Nodes, end)
			link(p, pp, end.ID)
			prev, prevPort = n.ID, on
		} else {
			prev, prevPort = n.ID, "success"
		}
	}
	link(prev, prevPort, first)
	return d
}

// applyAfterCards rebuilds everything after Save the result from cards.
func applyAfterCards(d Doc, w walked, cards []Card) Doc {
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	commit := w.by[TypeCommit]
	var startID string
	for _, e := range d.Edges {
		if e.From == commit && e.FromPort == "success" {
			startID = e.To
		}
	}
	kept := keep(d)
	gone := subtree(d, startID, map[string]bool{})
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return gone[n.ID] })
	d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool { return gone[e.From] || gone[e.To] || (e.From == commit && e.FromPort == "success") })
	var put func(from, port string, cards []Card)
	put = func(from, port string, cards []Card) {
		for _, c := range cards {
			n := nodeFor(c, byID)
			d.Nodes = append(d.Nodes, n)
			d.Edges = append(d.Edges, kept.link(from, port, n.ID))
			if IsQuestion(c.Type) {
				put(n.ID, "match", c.Yes)
				put(n.ID, "else", c.No)
				return
			}
			put(n.ID, "error", c.OnError)
			from, port = n.ID, "success"
		}
	}
	put(commit, "success", cards)
	return d
}

// applyFails writes what a converting step does when it doesn't work.
func applyFails(d Doc, id string, f *Fails) Doc {
	if f == nil {
		return d
	}
	var old string
	for _, e := range d.Edges {
		if e.From == id && e.FromPort == "error" {
			old = e.To
		}
	}
	typ := map[string]string{"leave": TypeEnd, "stop": TypeFail}[f.End]
	config := map[string]any{}
	if reason := strings.TrimSpace(f.Reason); reason != "" {
		config["reason"] = reason
	}
	if oldNode, ok := nodeByID(d, old); ok && oldNode.Type == typ { // the same kind of end as before: its node and link stay (QA-716)
		for i := range d.Nodes {
			if d.Nodes[i].ID == old {
				d.Nodes[i].Config = config
			}
			if d.Nodes[i].ID == id {
				d.Nodes[i].OnError = nil
			}
		}
		return d
	}
	if old != "" {
		if t, ok := nodeByID(d, old); ok && (t.Type == TypeEnd || t.Type == TypeFail) { // the guide's own end goes with the link
			d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return n.ID == old })
		}
		d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool { return e.From == id && e.FromPort == "error" })
	}
	for i := range d.Nodes {
		if d.Nodes[i].ID != id {
			continue
		}
		d.Nodes[i].OnError = nil
		switch f.End {
		case "leave", "stop":
			end := Node{ID: NewID(), Type: typ, TypeVersion: 1, Enabled: true, Config: config}
			d.Nodes = append(d.Nodes, end)
			d.Edges = append(d.Edges, Edge{ID: NewID(), From: id, FromPort: "error", To: end.ID, ToPort: "in"})
		case "retry":
			d.Nodes[i].OnError = &ErrorPolicy{Action: "retry", Max: f.Max, BackoffSeconds: f.BackoffSeconds, Then: f.Then}
		}
	}
	return d
}

func nodeByID(d Doc, id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// cardNames are the cards' step names, for Basic's notice.
func cardNames(cards []Card) []string {
	var out []string
	for _, c := range cards {
		out = append(out, nodeName(Node{Type: c.Type, Name: c.Name}))
	}
	return out
}
