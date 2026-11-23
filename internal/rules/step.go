// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "github.com/shrink-your-shit/shrink-your-shit/internal/media"

// Step is one part of a conversion's plan (W10, workflow-architecture §1.3). A step reads the file's stream
// inventory, changes its part of the shared Plan, and returns the Decisions that say what it did. Steps add to
// one Plan: the file still gets one ffmpeg process.
type Step interface {
	Apply(inv media.Info, p *Plan) []Decision
}

// runSteps applies the steps to p in order and collects their decisions in that order. A step that decides
// nothing adds nothing, so a Plan with no decisions stays as it was (its fingerprint does not change).
func runSteps(p Plan, inv media.Info, steps []Step) Plan {
	for _, s := range steps {
		p.Decisions = append(p.Decisions, s.Apply(inv, &p)...)
	}
	return p
}

// SelectorsStep is today's Selectors compile as the one built-in Step: the same Plan, byte for byte, so every
// stored flow keeps its fingerprint. It returns no decisions of its own.
type SelectorsStep struct {
	S     Selectors
	Langs []string
	Basic bool // a slider (Basic) flow: see compileSelectors
}

// Apply is compileSelectors on the Plan so far.
func (s SelectorsStep) Apply(inv media.Info, p *Plan) []Decision {
	*p = compileSelectors(*p, s.S, inv, s.Langs, s.Basic)
	return nil
}
