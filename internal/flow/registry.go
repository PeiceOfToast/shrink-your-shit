// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"fmt"
	"slices"
	"sync"
)

// The steps register themselves when flow/steps is imported, so flow can plan and validate a
// saved flow without importing the steps back (flow → flow/steps is the wrong direction).
var (
	stepMu       sync.RWMutex
	stepRegistry = map[string]Step{}
	stepOrder    []string // registration order, which is the order page 07 lists the steps
)

// RegisterStep adds a step under its type. A type registered twice is a programming mistake and
// panics, like a duplicated migration version.
func RegisterStep(s Step) {
	def := s.Def()
	if def.Type == "" {
		panic("flow: a step registered with no type")
	}
	stepMu.Lock()
	defer stepMu.Unlock()
	if _, dup := stepRegistry[def.Type]; dup {
		panic(fmt.Sprintf("flow: two steps registered as %q", def.Type))
	}
	stepRegistry[def.Type] = s
	stepOrder = append(stepOrder, def.Type)
}

// Definitions is every registered step in the flow's fixed order (Video, Audio, Save), which is
// also page 07's order. The flow editor builds its cards from these
// (GET /api/v2/step-definitions). Registration order is file order, so it cannot be the card order.
func Definitions() []Definition {
	stepMu.RLock()
	defer stepMu.RUnlock()
	out := make([]Definition, 0, len(stepOrder))
	for _, step := range fixedSteps {
		if s, ok := stepRegistry[step.Type]; ok {
			out = append(out, s.Def())
		}
	}
	for _, typ := range stepOrder {
		if !slices.ContainsFunc(fixedSteps, func(f struct{ ID, Type string }) bool { return f.Type == typ }) {
			out = append(out, stepRegistry[typ].Def())
		}
	}
	return out
}

// stepByType is the registered step for a type.
func stepByType(typ string) (Step, bool) {
	stepMu.RLock()
	defer stepMu.RUnlock()
	s, ok := stepRegistry[typ]
	return s, ok
}
