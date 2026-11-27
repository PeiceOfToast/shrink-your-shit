// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"encoding/json"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// flowWith is the starter with its three settings replaced by the strings given. Each string is
// what the page would save for that step.
func flowWith(t *testing.T, video, audio, save string) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	f := s.Flow
	f.Steps[0].Settings = json.RawMessage(video)
	f.Steps[1].Settings = json.RawMessage(audio)
	f.Steps[2].Settings = json.RawMessage(save)
	return f
}

// TestValidateStarterHasNoProblems is the stop-1 promise at the check: the starter the setup call
// writes validates clean, and its closed cards carry page 07's lines.
func TestValidateStarterHasNoProblems(t *testing.T) {
	rep := flow.Validate(starterFlow(t), flow.ValidateContext{})
	if len(rep.Problems) != 0 {
		t.Errorf("problems = %+v, want none for the starter", rep.Problems)
	}
	want := map[string]string{
		"video": "HEVC · quality 3 · graphics card off",
		"audio": "Same as the source · same channels as the source · my languages · subtitles: keep all",
		"save":  "MKV · next to the original",
	}
	for id, text := range want {
		if rep.Summaries[id] != text {
			t.Errorf("%s summary = %q, want %q", id, rep.Summaries[id], text)
		}
	}
	// The starter's HEVC picture and copied audio cannot go into WebM, so that choice is greyed on
	// the Save card even though the flow is fine as it is.
	found := false
	for _, d := range rep.Disabled {
		if d.StepID == "save" && d.Field == "container" && d.Value == "webm" {
			found = d.Why == "WebM needs AV1 video and Opus audio."
		}
	}
	if !found {
		t.Errorf("disabled = %+v, want the WebM choice greyed with its reason", rep.Disabled)
	}
	// Replacing the original is item 10's; the page greys that choice with a plain why.
	found = false
	for _, d := range rep.Disabled {
		if d.StepID == "save" && d.Field == "where" && d.Value == "replace" {
			found = d.Why != ""
		}
	}
	if !found {
		t.Errorf("disabled = %+v, want Replace the original greyed with its reason", rep.Disabled)
	}
}

// TestValidateSummariesForANonDefaultFlow pins the other closed-card lines: another format and
// quality, a graphics card, chosen languages, and a replaced original.
func TestValidateSummariesForANonDefaultFlow(t *testing.T) {
	f := flowWith(t,
		`{"format":"h264","quality":5,"useGpu":true,"ifGpuFails":"cpu"}`,
		`{"format":"aac","channels":"stereo","languages":["eng","ger"]}`,
		`{"container":"mp4","where":"replace"}`)
	rep := flow.Validate(f, flow.ValidateContext{})
	want := map[string]string{
		"video": "H.264 · quality 5 · graphics card automatic",
		"audio": "AAC · stereo · English and German · subtitles: keep all",
		"save":  "MP4 · replace the original · old file in the trash 7 days",
	}
	for id, text := range want {
		if rep.Summaries[id] != text {
			t.Errorf("%s summary = %q, want %q", id, rep.Summaries[id], text)
		}
	}
}

// TestContainerRulesAreTheFlows checks each pair page 07 names: the choice that cannot work is
// greyed, and the flow gets a problem only when that choice is already the saved one.
func TestContainerRulesAreTheFlows(t *testing.T) {
	cases := []struct {
		name        string
		format      string
		audio       string
		container   string
		disabled    string // the container value that must be greyed, "" for none
		wantProblem bool
	}{
		{"webm with HEVC", "hevc", `{"format":"copy"}`, "webm", "webm", true},
		{"webm with AV1 and copied audio", "av1", `{"format":"copy"}`, "webm", "webm", true},
		{"webm with AV1 and Opus", "av1", `{"format":"opus"}`, "webm", "", false},
		{"mp4 with Opus", "hevc", `{"format":"opus"}`, "mp4", "mp4", true},
		{"mp4 with AAC", "hevc", `{"format":"aac"}`, "mp4", "", false},
		{"mp4 with copied audio", "hevc", `{"format":"copy"}`, "mp4", "", false},
		{"mkv with anything", "hevc", `{"format":"opus"}`, "mkv", "", false},
		{"keep with anything", "hevc", `{"format":"opus"}`, "keep", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := flowWith(t,
				`{"format":"`+c.format+`","quality":3,"useGpu":false,"ifGpuFails":"cpu"}`,
				c.audio, `{"container":"`+c.container+`"}`)
			rep := flow.Validate(f, flow.ValidateContext{})

			gotDisabled := false
			for _, d := range rep.Disabled {
				if d.StepID == "save" && d.Field == "container" && d.Value == c.disabled && c.disabled != "" {
					gotDisabled = d.Why != ""
				}
			}
			if c.disabled != "" && !gotDisabled {
				t.Errorf("disabled = %+v, want %s greyed", rep.Disabled, c.disabled)
			}
			// The picked file type is not greyed; another one may still be (WebM stays greyed for a
			// HEVC flow), which is the page's "this pair cannot go in there" note.
			if c.disabled == "" {
				for _, d := range rep.Disabled {
					if d.StepID == "save" && d.Field == "container" && d.Value == c.container {
						t.Errorf("disabled = %+v, want %s not greyed", rep.Disabled, c.container)
					}
				}
			}

			var problem *flow.Problem
			for i := range rep.Problems {
				if rep.Problems[i].Code == "container_unsupported" {
					problem = &rep.Problems[i]
				}
			}
			if !c.wantProblem {
				if problem != nil {
					t.Errorf("problem = %+v, want none for %s", *problem, c.container)
				}
				return
			}
			if problem == nil {
				t.Fatalf("problems = %+v, want container_unsupported", rep.Problems)
			}
			if problem.StepID != "save" || problem.Field != "container" ||
				problem.Message == "" || problem.Message[len(problem.Message)-1] != '.' {
				t.Errorf("problem = %+v, want it on Save's container with a plain sentence", *problem)
			}
		})
	}
}

// TestValidateAttachesStepIDs checks that a step's own problem and greyed field come back with the
// step's id, which is what lets the page mark the right card.
func TestValidateAttachesStepIDs(t *testing.T) {
	f := flowWith(t,
		`{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu","rateControl":"vbr"}`,
		`{"format":"copy"}`, `{"container":"mkv"}`)
	rep := flow.Validate(f, flow.ValidateContext{})

	found := false
	for _, p := range rep.Problems {
		if p.Field == "rateControl" {
			found = p.StepID == "video"
		}
	}
	if !found {
		t.Errorf("problems = %+v, want the rateControl problem on the video step", rep.Problems)
	}
	found = false
	for _, d := range rep.Disabled {
		if d.Field == "quality" {
			found = d.StepID == "video" && d.Why != ""
		}
	}
	if !found {
		t.Errorf("disabled = %+v, want quality greyed on the video step", rep.Disabled)
	}
}

// TestValidateFlowShapeHasNoStep checks that a problem about the whole flow carries no step id:
// the page shows it on the head's check line.
func TestValidateFlowShapeHasNoStep(t *testing.T) {
	f := flowWith(t, `{}`, `{}`, `{}`)
	f.Steps = f.Steps[:2]
	rep := flow.Validate(f, flow.ValidateContext{})
	if len(rep.Problems) != 1 || rep.Problems[0].Code != "flow.invalid" || rep.Problems[0].StepID != "" {
		t.Errorf("problems = %+v, want one flow.invalid problem with no step", rep.Problems)
	}
}
