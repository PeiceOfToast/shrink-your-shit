// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Flow is one revision's saved shape (flow-model.md): three fixed steps, always in this order.
type Flow struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Steps       []StepRef `json:"steps"`
}

// StepRef is one step in a flow: its fixed id, its type, the settings shape's version and the
// settings themselves.
type StepRef struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Version  int             `json:"version"`
	Settings json.RawMessage `json:"settings"`
}

// fixedSteps is the order, ids and types of the three steps (flow-model.md). Every other list is
// refused: problems, Try results and the log all use these ids.
var fixedSteps = []struct{ ID, Type string }{
	{"video", "video.convert"},
	{"audio", "audio.tracks"},
	{"save", "file.save"},
}

// Report is page 07's answer to a check: what blocks publishing, what is greyed, and the closed
// cards' one-line summaries, by step id (ui/src/api/flowTypes.ts's FlowCheck).
type Report struct {
	Problems  []Problem         `json:"problems"`
	Disabled  []Disabled        `json:"disabled"`
	Summaries map[string]string `json:"summaries"`
}

// OK reports whether the flow may be published.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Validate runs the three steps' own checks and adds the flow-level ones: exactly three steps, in
// the fixed order, with known types and versions, and the rules that cross steps (the container a
// video format and an audio codec can go into). A step's problems and greyed fields come back with
// its id attached, so the page can mark the right card.
func Validate(f Flow, vc ValidateContext) Report {
	// Empty, never null: the page reads both lists and draws them as they are.
	rep := Report{Problems: []Problem{}, Disabled: []Disabled{}, Summaries: map[string]string{}}
	if problems := shapeProblems(f); len(problems) > 0 {
		for _, text := range problems {
			rep.Problems = append(rep.Problems, Problem{Code: "flow.invalid", Message: text})
		}
		return rep
	}
	decoded := make([]Settings, 0, len(f.Steps))
	for _, ref := range f.Steps {
		step, settings, err := decodeStep(ref)
		if err != nil {
			rep.Problems = append(rep.Problems, Problem{
				StepID: ref.ID, Field: "settings", Code: "setting.invalid", Message: err.Error(),
			})
			continue
		}
		decoded = append(decoded, settings)
		check := step.Validate(settings, vc)
		for _, p := range check.Problems {
			if p.Code == "" {
				p.Code = "step.invalid"
			}
			p.StepID = ref.ID
			rep.Problems = append(rep.Problems, p)
		}
		for _, d := range check.Disabled {
			d.StepID = ref.ID
			rep.Disabled = append(rep.Disabled, d)
		}
		rep.Summaries[ref.ID] = check.Summary
	}
	// The container rules are the flow's, not a step's: they need the format Video asks for and
	// the codec Audio will write as well as the file type Save chose (page 07).
	rep.Disabled = append(rep.Disabled, containerDisabled(decoded)...)
	rep.Problems = append(rep.Problems, containerProblems(decoded)...)
	return rep
}

// CheckShape refuses a flow that is not the three fixed steps (the 400 invalid_flow of POST
// /workflows and PUT /workflows/{id}/draft). Settings are not checked here: a draft may hold a
// "not yet" value, and the check is what blocks publishing it.
func CheckShape(f Flow) error {
	if problems := shapeProblems(f); len(problems) > 0 {
		return errors.New(problems[0])
	}
	return nil
}

// shapeProblems is the flow-level check: the three steps, in order, with the types the ids name.
func shapeProblems(f Flow) []string {
	if len(f.Steps) != len(fixedSteps) {
		return []string{"A workflow has exactly three steps: Video, Audio and Save."}
	}
	var problems []string
	for i, want := range fixedSteps {
		ref := f.Steps[i]
		if ref.ID != want.ID || ref.Type != want.Type {
			problems = append(problems, fmt.Sprintf("Step %d must be %s (%s), not %s (%s).",
				i+1, want.Type, want.ID, ref.Type, ref.ID))
			continue
		}
		if ref.Version <= 0 {
			problems = append(problems, fmt.Sprintf("%s has no settings version.", ref.ID))
		}
	}
	return problems
}

// stepNotRegistered is returned when no step answers for a type. It names the wiring mistake: the
// program imports flow/steps, which registers the three in its init.
var stepNotRegistered = errors.New("no step is registered for this type")

// decodeStep finds the step for one saved reference and decodes its settings, moving an older
// settings shape forward through the step's Upgrade first.
func decodeStep(ref StepRef) (Step, Settings, error) {
	step, ok := stepByType(ref.Type)
	if !ok {
		return nil, nil, fmt.Errorf("%s %q: %w", ref.ID, ref.Type, stepNotRegistered)
	}
	def := step.Def()
	if ref.Version > def.Version {
		return nil, nil, fmt.Errorf("%s was saved by a newer version of this program (settings version %d, this one knows %d)",
			ref.ID, ref.Version, def.Version)
	}
	raw := ref.Settings
	if ref.Version < def.Version {
		raw = upgraded(def, raw, ref.Version)
		if raw == nil {
			return nil, nil, fmt.Errorf("%s is from an older version with no upgrade path", ref.ID)
		}
	}
	settings, err := DecodeSettings(def, raw)
	if err != nil {
		return nil, nil, err
	}
	return step, settings, nil
}

// upgraded moves a saved settings map through the step's Upgrade. It returns nil when the step
// has no upgrade path.
func upgraded(def Definition, raw json.RawMessage, from int) json.RawMessage {
	if def.Upgrade == nil {
		return nil
	}
	var old map[string]any
	if err := json.Unmarshal(raw, &old); err != nil {
		return nil
	}
	moved := def.Upgrade(old, from)
	text, err := json.Marshal(moved)
	if err != nil {
		return nil
	}
	return text
}

// PlanError is a step's reason to refuse this file or these settings. The engine turns it into a
// failed job with the same code, sentence and next action.
type PlanError struct {
	Step       string
	Code       string
	Message    string
	NextAction string
}

func (e *PlanError) Error() string { return e.Message }

// planError tags an error from a step's Plan with the step that raised it; an error that already
// says what to do keeps its own words.
func planError(step string, err error) *PlanError {
	var pe *PlanError
	if errors.As(err, &pe) {
		if pe.Step == "" {
			pe.Step = step
		}
		return pe
	}
	return &PlanError{Step: step, Code: "step.failed", Message: err.Error()}
}

// DecodeStep reads one step of a flow: the step and its typed settings. The steps' sentences (page 09) read a flow
// through it, so they see the settings the run sees.
func DecodeStep(ref StepRef) (Step, Settings, error) {
	return decodeStep(ref)
}
