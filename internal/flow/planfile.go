// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"errors"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// StepDecision is one step's plan result, with the words the log needs to frame it.
type StepDecision struct {
	ID       string
	Type     string
	Name     string
	Version  int
	Index    int
	Decision Decision
}

// Decisions is what every step decided for this file, in the flow's order.
type Decisions struct {
	Steps []StepDecision
}

// Work reports whether any step has something to do for this file.
func (d Decisions) Work() bool {
	for _, s := range d.Steps {
		if s.Decision.Work {
			return true
		}
	}
	return false
}

// Reasons lists every step's reason code, for the skip line.
func (d Decisions) Reasons() []string {
	out := make([]string, 0, len(d.Steps))
	for _, s := range d.Steps {
		out = append(out, s.Decision.Reason)
	}
	return out
}

// PlanFile plans a whole revision against one probed file. It is pure: the same revision, info and
// env always give the same decisions and encode plan, which is what lets the scan preview and the
// job agree. It never runs ffmpeg and never touches a file.
func PlanFile(rev Revision, info media.Info, env Env) (Decisions, encode.Plan, error) {
	var d Decisions
	var plan encode.Plan
	if problems := shapeProblems(rev.Flow); len(problems) > 0 {
		return d, plan, &PlanError{Code: "invalid_flow", Message: strings.Join(problems, " ")}
	}
	for i, ref := range rev.Flow.Steps {
		step, settings, err := decodeStep(ref)
		if err != nil {
			return d, plan, &PlanError{Step: ref.ID, Code: "invalid_settings", Message: err.Error()}
		}
		decision, err := step.Plan(settings, PlanInput{Info: info, Env: env}, &plan)
		if err != nil {
			return d, plan, planError(ref.ID, err)
		}
		d.Steps = append(d.Steps, StepDecision{
			ID: ref.ID, Type: ref.Type, Name: step.Def().Name, Version: ref.Version,
			Index: i + 1, Decision: decision,
		})
	}
	return d, plan, nil
}

// planErrorCode is the reason code a plan error carries, with "step.failed" when it names none.
func planErrorCode(err error) string {
	var pe *PlanError
	if errors.As(err, &pe) && pe.Code != "" {
		return pe.Code
	}
	return "step.failed"
}
