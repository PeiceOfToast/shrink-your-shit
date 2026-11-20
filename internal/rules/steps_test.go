// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"
)

func stepLabels(ps []StepDecision) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Label
	}
	return out
}

// L2: every step that runs says what it decided and why, in the workflow's own step names, in run order.
func TestStepsOfAConversion(t *testing.T) {
	tmpl := SliderTemplate(3)
	tmpl.Languages = "eng"
	p := tmpl.Compile(guardFiles()["h264-1080-mkv"])
	if p.Skip != "" {
		t.Fatalf("this file should convert: %s", p.Skip)
	}
	want := []string{StepConvertVideo, StepAudio, StepSubtitles, StepFileType}
	if got := stepLabels(p.Steps); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("steps %v, want %v", got, want)
	}
	for _, st := range p.Steps {
		if st.Outcome != "ran" {
			t.Errorf("%s: outcome %q, want ran", st.Label, st.Outcome)
		}
		if st.Decision == "" || !strings.HasSuffix(st.Decision, ".") || !strings.HasPrefix(st.Decision, strings.ToUpper(st.Decision[:1])) {
			t.Errorf("%s: decision %q is not one sentence", st.Label, st.Decision)
		}
	}
	video := p.Steps[0].Decision
	for _, part := range []string{"H.264", "becomes HEVC", "CRF"} {
		if !strings.Contains(video, part) {
			t.Errorf("video decision %q lacks %q (R3: the rule and its numbers)", video, part)
		}
	}
}

// A step that did nothing still says so (R4): a file with no audio or subtitles gets those lines too.
func TestStepsOfAFileWithNoAudioOrSubtitles(t *testing.T) {
	info := guardFiles()["h264-1080-mkv"]
	info.Streams = info.Streams[:1] // the video only
	p := SliderTemplate(3).Compile(info)
	if len(p.Steps) != 4 {
		t.Fatalf("%d steps, want 4", len(p.Steps))
	}
	if p.Steps[1].Decision != "There is no audio." || p.Steps[2].Decision != "There are no subtitle tracks." {
		t.Fatalf("audio %q, subtitles %q", p.Steps[1].Decision, p.Steps[2].Decision)
	}
}

// A file the flow leaves alone gets its one step, marked skipped, with the reason (R4).
func TestStepsOfASkippedFile(t *testing.T) {
	p := SliderTemplate(3).Compile(guardFiles()["av1-mp4"])
	if p.Skip == "" {
		t.Fatal("this file should be skipped")
	}
	if len(p.Steps) != 1 || p.Steps[0].Label != StepLeftAlone || p.Steps[0].Outcome != "skipped" {
		t.Fatalf("steps %+v", p.Steps)
	}
	if !strings.Contains(p.Steps[0].Decision, "modern format") {
		t.Fatalf("decision %q does not say why", p.Steps[0].Decision)
	}
}

// A legacy flow has one step: its video settings (the audio and subtitles are copied as before).
func TestStepsOfALegacyFlow(t *testing.T) {
	p := LegacyTemplate(Default(), "replace").Compile(guardFiles()["h264-1080-mkv"])
	if p.Skip != "" {
		t.Fatalf("this file should convert: %s", p.Skip)
	}
	if len(p.Steps) != 1 || p.Steps[0].Label != StepConvertVideo {
		t.Fatalf("steps %+v", p.Steps)
	}
}

// A workflow's check answers are steps too, before the path's own steps (the worker numbers the repeats, R12).
func TestStepsOfAWorkflowWithChecks(t *testing.T) {
	then := SliderTemplate(3)
	checks := []Branch{{Path: []string{"in", "check", "out"}, Checks: []BranchCheck{
		{NodeID: "check", Port: "match", Check: VideoCheck{Name: "Is it 4K?", Resolution: 2160}},
		{NodeID: "check", Port: "match", Check: VideoCheck{Name: "Is it HDR?", HDR: "yes"}},
	}, Then: &then}}
	tmpl := Template{Kind: KindBranches, Branches: &checks}
	p := tmpl.Compile(guardFiles()["hevc-4k-hdr"])
	n := 0
	for _, st := range p.Steps {
		if st.Label == StepCheck {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("steps %+v: want the two check answers", p.Steps)
	}
	if !strings.HasPrefix(p.Steps[0].Decision, "Is it 4K?") {
		t.Fatalf("first decision %q", p.Steps[0].Decision)
	}
}

func TestCheckStepsAreSentences(t *testing.T) {
	ds := []Decision{{Words: "Is it 4K? → Yes (resolution is 2160p)"}}
	got := checkSteps(ds)
	if len(got) != 1 || got[0].Decision != "Is it 4K? → Yes (resolution is 2160p)." {
		t.Fatalf("got %+v", got)
	}
}
