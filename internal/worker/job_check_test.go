// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-13 item E: the encode step carries the source's probe summary, and the picture check is a step of its
// own with its score, the least it needed and whether it passed.
func TestCheckStepAndProbeAreInTheLog(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.80 U:0.80 V:0.80 All:0.800000 (7.0)"
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
	w.RunOne(context.Background())
	steps, _ := s.store.RunSteps(s.id)
	var check, encode *jobs.RunStep
	for i := range steps {
		switch steps[i].Name {
		case "check":
			check = &steps[i]
		case "encode":
			encode = &steps[i]
		}
	}
	if check == nil || check.CheckScore == nil || *check.CheckScore < 0.79 || *check.CheckScore > 0.81 || check.CheckPassed == nil || *check.CheckPassed {
		t.Fatalf("check step %+v", check)
	}
	if encode == nil || !strings.Contains(encode.Probe, `"codec"`) {
		t.Fatalf("encode step has no probe: %+v", encode)
	}
}

func TestProbeSummary(t *testing.T) {
	info := media.Info{BitrateKbps: 8000, DurationSec: 120, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 10, ColorTransfer: "smpte2084"},
		{Index: 1, Type: "audio", Codec: "aac"}, {Index: 2, Type: "audio", Codec: "ac3"}, {Index: 3, Type: "subtitle", Codec: "subrip"},
	}}
	var s struct {
		Codec        string `json:"codec"`
		Width        int    `json:"width"`
		HDR          bool   `json:"hdr"`
		AudioTracks  int    `json:"audioTracks"`
		SubtitleTrks int    `json:"subtitleTracks"`
		BitrateKbps  int    `json:"bitrateKbps"`
	}
	if err := json.Unmarshal([]byte(probeSummary(info)), &s); err != nil {
		t.Fatal(err)
	}
	if s.Codec != "hevc" || s.Width != 1920 || !s.HDR || s.AudioTracks != 2 || s.SubtitleTrks != 1 || s.BitrateKbps != 8000 {
		t.Errorf("summary %+v", s)
	}
}
