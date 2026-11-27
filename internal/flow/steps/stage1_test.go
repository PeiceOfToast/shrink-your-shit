// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestStageOneWorkflowsStillPlanTheSame is the stop-1 promise: a workflow the user saved in stage 1,
// stored exactly as stage 1 wrote it, still decodes, plans and validates to the same answer now
// that page 07's new settings exist. The settings strings below are stage 1's own, not this
// stage's defaults, so a change to a default cannot make this test pass by accident.
func TestStageOneWorkflowsStillPlanTheSame(t *testing.T) {
	rev := flow.Revision{WorkflowID: 1, Rev: 1, Flow: flow.Flow{
		Name: "Smaller",
		Steps: []flow.StepRef{
			{ID: "video", Type: "video.convert", Version: 1,
				Settings: json.RawMessage(`{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu"}`)},
			{ID: "audio", Type: "audio.tracks", Version: 1,
				Settings: json.RawMessage(`{"format":"copy"}`)},
			{ID: "save", Type: "file.save", Version: 1,
				Settings: json.RawMessage(`{"container":"mkv","where":"next_to","nameSuffix":"shrunk"}`)},
		},
	}}
	info := media.Info{
		Path:   "/library/clip.mkv",
		Format: media.Format{Names: []string{"matroska"}},
		Video:  []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080}},
	}

	decisions, plan, err := flow.PlanFile(rev, info, flow.Env{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Video.Codec != encode.CodecHEVC || plan.Video.CRF == nil || *plan.Video.CRF != 23 {
		t.Errorf("video plan = %+v, want HEVC CRF 23", plan.Video)
	}
	if len(plan.Audio) != 0 || len(plan.Subtitles) != 0 {
		t.Errorf("audio = %+v, subtitles = %+v, want the source copied as is", plan.Audio, plan.Subtitles)
	}
	if plan.Container != encode.ContainerMKV {
		t.Errorf("container = %q, want mkv", plan.Container)
	}
	if got := filepath.ToSlash(plan.Output); got != "/library/clip-shrunk.mkv" {
		t.Errorf("output = %q, want /library/clip-shrunk.mkv", got)
	}
	wantReasons := []string{"video.convert", "audio.as_is", "save.next_to"}
	if got := decisions.Reasons(); len(got) != len(wantReasons) {
		t.Errorf("reasons = %v, want %v", got, wantReasons)
	} else {
		for i, want := range wantReasons {
			if got[i] != want {
				t.Errorf("reasons = %v, want %v", got, wantReasons)
				break
			}
		}
	}

	// Nothing in a stage 1 workflow was a "not yet" setting, and nothing new may refuse it.
	if report := flow.Validate(rev.Flow, flow.ValidateContext{}); len(report.Problems) != 0 {
		t.Errorf("problems = %+v, want none for a stage 1 workflow", report.Problems)
	}
}
