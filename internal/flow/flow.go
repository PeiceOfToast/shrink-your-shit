// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package flow runs the cards of a workflow path that sit outside the conversion (docs/architecture/
// flow-builder.md, section 6): the actions before it (FB1c) and, later, the actions after the save
// (FB2). It has no storage and no HTTP; the worker gives it the run's state and what the actions may
// touch, and records each card's words as a step of the job log.
package flow

import (
	"fmt"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// State is one run's state as the cards see it.
type State struct {
	Vars map[string]string // the built-ins, the workflow's own variables, and what cards set (vars.Resolve's values)
}

// Env is what actions may touch; the worker fills it. A nil function means the action can't run here.
type Env struct {
	Log func(line string) // a line into the job log (Add a note)
}

// Note is what one card did, in words, for the job log's timeline.
type Note struct {
	NodeID string
	Name   string
	Words  string
	Empty  []string // the {{name}} references that had no value
}

// Before runs the path's actions before the conversion, in order. The first action that doesn't work
// stops the run: the worker fails the job with the error's words, the original untouched (section 1.5).
func Before(cards []rules.Card, st *State, env Env) ([]Note, error) {
	if st.Vars == nil {
		st.Vars = map[string]string{}
	}
	var out []Note
	for _, c := range cards {
		n, err := run(c, st, env)
		if err != nil {
			return out, fmt.Errorf("%s didn't work: %w", nameOf(c), err)
		}
		out = append(out, n)
	}
	return out, nil
}

// run does one card.
func run(c rules.Card, st *State, env Env) (Note, error) {
	n := Note{NodeID: c.NodeID, Name: nameOf(c)}
	switch c.Type {
	case workflow.TypeSetVariable:
		name, _ := c.Config["name"].(string)
		if !workflow.NamePattern.MatchString(name) || workflow.BuiltinName(name) {
			return n, fmt.Errorf("%q is not a name a step may set", name)
		}
		tpl, _ := c.Config["value"].(string)
		val, empty := vars.Resolve(tpl, st.Vars)
		st.Vars[name] = val
		n.Words, n.Empty = "Set {{"+name+"}} = "+quote(val), empty
		return n, nil
	case workflow.TypeNote:
		tpl, _ := c.Config["text"].(string)
		text, empty := vars.Resolve(tpl, st.Vars)
		if env.Log != nil {
			env.Log(text)
		}
		n.Words, n.Empty = "Note: "+quote(text), empty
		return n, nil
	}
	return n, fmt.Errorf("a step this version can't run (%s)", c.Type)
}

func nameOf(c rules.Card) string {
	if c.Name != "" {
		return c.Name
	}
	if d, ok := workflow.Definition(c.Type); ok {
		return d.Name
	}
	return c.Type
}

func quote(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:117]) + "..."
	}
	return "“" + s + "”"
}
