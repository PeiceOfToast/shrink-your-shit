// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"fmt"
	"sync"
)

// The steps register themselves when flow/steps is imported, so flow can plan and validate a
// saved flow without importing the steps back (flow → flow/steps is the wrong direction).
var (
	stepMu       sync.RWMutex
	stepRegistry = map[string]Step{}
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
}

// stepByType is the registered step for a type.
func stepByType(typ string) (Step, bool) {
	stepMu.RLock()
	defer stepMu.RUnlock()
	s, ok := stepRegistry[typ]
	return s, ok
}
