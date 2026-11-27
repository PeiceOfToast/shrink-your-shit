// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"errors"
	"slices"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// codeCase is one input that makes a step say one code: a decision's reason or a plan error's
// code. Together these must cover every EventDef a step declares, and nothing else.
type codeCase struct {
	name  string
	step  flow.Step
	set   flow.Settings
	info  media.Info
	plan  encode.Plan
	want  string
	isErr bool
}

func codeCases() []codeCase {
	working := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}
	return []codeCase{
		{name: "video convert", step: VideoStep{}, set: videoSettings("hevc", 3), info: probeInfo("h264", 1920, 1080), want: "video.convert"},
		{name: "video already target", step: VideoStep{}, set: videoSettings("hevc", 3), info: probeInfo("hevc", 1920, 1080), want: "video.already_target"},
		{name: "video no picture", step: VideoStep{}, set: videoSettings("hevc", 3),
			info: media.Info{Path: "/library/a.mkv"}, want: "video.no_video", isErr: true},
		{name: "audio as is", step: AudioStep{}, set: AudioSettings{Format: "copy"}, info: probeInfo("h264", 1920, 1080), want: "audio.as_is"},
		{name: "save next to", step: SaveStep{}, set: saveSettings("mkv", "next_to", "shrunk"), info: mp4Info(), plan: working, want: "save.next_to"},
		{name: "save nothing", step: SaveStep{}, set: saveSettings("mkv", "next_to", "shrunk"), info: mp4Info(), want: "save.nothing"},
		{name: "save lands on source", step: SaveStep{}, set: saveSettings("keep", "next_to", ""),
			info: media.Info{Path: "/library/a.mkv", Format: media.Format{Names: []string{"matroska"}}},
			plan: working, want: "save.would_land_on_source", isErr: true},
		{name: "save unknown container", step: SaveStep{}, set: saveSettings("keep", "next_to", "x"),
			info: media.Info{Path: "/library/a.avi", Format: media.Format{Names: []string{"avi"}}},
			plan: working, want: "save.unknown_container", isErr: true},
	}
}

// TestStepEventCodesAreDeclaredAndCovered is the contract's event test: a code a step can say is
// declared in its Definition, and every declared code is made by a case here.
func TestStepEventCodesAreDeclaredAndCovered(t *testing.T) {
	made := map[string]map[string]bool{}
	for _, c := range codeCases() {
		p := c.plan
		got := c.want
		var err error
		if c.isErr {
			var pe *flow.PlanError
			_, err = c.step.Plan(c.set, flow.PlanInput{Info: c.info}, &p)
			if !errors.As(err, &pe) {
				t.Errorf("%s: error = %v, want a plan error", c.name, err)
				continue
			}
			got = pe.Code
		} else {
			var d flow.Decision
			d, err = c.step.Plan(c.set, flow.PlanInput{Info: c.info}, &p)
			if err != nil {
				t.Errorf("%s: plan: %v", c.name, err)
				continue
			}
			got = d.Reason
		}
		if got != c.want {
			t.Errorf("%s: code = %q, want %q", c.name, got, c.want)
			continue
		}
		typ := c.step.Def().Type
		if made[typ] == nil {
			made[typ] = map[string]bool{}
		}
		made[typ][got] = true
	}

	for _, step := range []flow.Step{VideoStep{}, AudioStep{}, SaveStep{}} {
		typ := step.Def().Type
		declared := make([]string, 0, len(step.Def().Events))
		for _, ev := range step.Def().Events {
			declared = append(declared, ev.Code)
			if !made[typ][ev.Code] {
				t.Errorf("%s declares %q but no case makes it", typ, ev.Code)
			}
			if ev.Text == "" {
				t.Errorf("%s: %q has no text", typ, ev.Code)
			}
		}
		for code := range made[typ] {
			if !slices.Contains(declared, code) {
				t.Errorf("%s makes %q but does not declare it", typ, code)
			}
		}
	}
}
