// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// Settings is a step's decoded settings: a value of the step's own settings struct, filled from the
// saved JSON with the defaults applied.
type Settings any

// Step is the contract one workflow step meets (step-interface.md). There are three, fixed.
type Step interface {
	// Def is the step's own description: its type, version, words and the codes it can emit.
	Def() Definition
	// Plan looks at the probed file and adds this step's part to the encode plan. It never runs
	// ffmpeg and never touches a file, so the scan preview and the job always agree.
	Plan(s Settings, in PlanInput, p *encode.Plan) (Decision, error)
	// Validate checks settings in the context of the whole flow and this server.
	Validate(s Settings, vc ValidateContext) Check
}

// Definition describes a step. Type never changes; Version is the settings shape, and a change to
// it comes with an Upgrade that moves an older saved shape forward (step-interface.md).
type Definition struct {
	Type     string
	Version  int
	Name     string
	Help     string
	Settings any
	// Events is every code this step's decisions and warnings can carry; a test fails on a code
	// that is emitted but not declared, and on a declared code no case makes.
	Events  []EventDef
	Upgrade func(old map[string]any, from int) map[string]any
}

// EventDef is one code a step can put in an event, with the words the log builds from it.
type EventDef struct {
	Code   string
	Level  events.Level
	Text   string
	Fields []string
	Keep   events.Keep
}

// PlanInput is what a step plans against: the probe of the source and the world around it.
type PlanInput struct {
	Info media.Info
	Env  Env
}

// Decision is what one step decided for this file. Work false is the step's "nothing to do".
type Decision struct {
	Work   bool
	Label  string // the words the log and the step card show, e.g. "Already HEVC"
	Reason string // the typed code, e.g. "video.already_target"
	Detail F      // fields for the log: from/to codec, the chosen rung …
}

// F is a small set of named fields for an event's data.
type F map[string]any

// Check is a step's validation answer: what blocks publishing, what is greyed, and the closed
// card's one line.
type Check struct {
	Problems []Problem
	Disabled []Disabled
	Summary  string
}

// Problem is one thing that blocks publishing. Field names the setting, when it is one setting's
// fault, so the page can mark it.
type Problem struct {
	Field   string
	Code    string
	Message string
}

// Disabled greys one field, or one choice of it, with the reason. Value empty means the whole
// field.
type Disabled struct {
	Field string
	Value string
	Why   string
}

// ValidateContext is the whole flow as validated, and the server it is checked against.
type ValidateContext struct {
	Flow Flow
	Env  Env
}
