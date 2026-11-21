// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// hdSpec is a 1920x1080 file with one audio track, in the given codec/pixel format, as the worker probes
// the original and as the plan makes the new file.
func hdSpec(codec, pix string, w, h int) spec {
	return spec{duration: 100, streams: []map[string]any{vid(codec, w, h, pix), aud("aac", "eng", 2)}}
}

// hdFlow is a Standard flow in format at resolution (a largest height, or "keep").
func hdFlow(format, resolution string) rules.Template {
	s := rules.DefaultSelectors(3)
	s.Format, s.Resolution = format, resolution
	return s.Template()
}

// Foundation slice 3's worker half: a picture that is not exactly the prediction but is the picture the
// plan makes passes (852x480 and 854x480 for 854 planned); one of another shape is kept, and the refusal
// names the rule, not the original.
func TestCheckJudgesTheScaledPictureByItsRules(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		done bool
	}{
		{"852x480 is the planned picture", 852, 480, true},
		{"854x480 is the planned picture", 854, 480, true},
		{"640x480 is another picture", 640, 480, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			s.srcSpec = hdSpec("h264", "yuv420p", 1920, 1080)
			s.outSpec = hdSpec("hevc", "yuv420p", c.w, c.h)
			w := s.worker(Replace)
			w.Template = func(jobs.Job) (rules.Template, int64, error) { return hdFlow("hevc", "480"), 0, nil }
			w.RunOne(context.Background()) // a kept refusal is this test's point too
			j := s.job()
			if done := j.Status == jobs.Done; done != c.done {
				t.Fatalf("status %s (error %q), want done %v", j.Status, j.Error, c.done)
			}
			if !strings.Contains(j.Plan, `"MainVideo"`) || !strings.Contains(j.Plan, `"Height":480`) || !strings.Contains(j.Plan, `"Width":854`) {
				t.Errorf("the recorded plan does not show the picture's rules: %s", j.Plan)
			}
			if c.done {
				return
			}
			if want := "the plan makes it 480 lines high with the original's shape (854x480, ±2)"; !strings.Contains(j.Error, want) {
				t.Errorf("the refusal does not name the rule: %q", j.Error)
			}
			s.assertOriginalSafe()
		})
	}
}

// The depth is judged by the encoder that ran: for the same 10-bit source and H.264 plan the card makes
// 8 bits and the processor makes 10; each one's own depth passes, the other is kept.
func TestCheckJudgesTheDepthOfTheEncoderThatRan(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx264": true})
	defer rules.SetAvailableFormats(nil)
	for _, c := range []struct {
		name  string
		gpu   string
		pix   string // the new file's
		done  bool
		words string
	}{
		{name: "the card makes 8 bits", gpu: "h264_qsv", pix: "yuv420p", done: true},
		{name: "the processor makes 10 bits", gpu: "", pix: "yuv420p10le", done: true},
		{name: "the card's 10-bit file is not what it makes", gpu: "h264_qsv", pix: "yuv420p10le",
			words: "the colour depth is 10-bit; h264_qsv makes 8-bit for this file"},
		{name: "the processor's 8-bit file is not what it makes", gpu: "", pix: "yuv420p",
			words: "the colour depth is 8-bit; the processor makes 10-bit for this file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			s.srcSpec = hdSpec("hevc", "yuv420p10le", 1920, 1080) // 10-bit SDR, converted to H.264
			s.outSpec = hdSpec("h264", c.pix, 1920, 1080)
			w := s.worker(Replace)
			w.Template = func(jobs.Job) (rules.Template, int64, error) { return hdFlow("h264", "keep"), 0, nil }
			w.Encoder = func(string, bool, *rules.HDR) (string, string) { return c.gpu, "" }
			w.RunOne(context.Background()) // a kept refusal is this test's point too
			j := s.job()
			if done := j.Status == jobs.Done; done != c.done {
				t.Fatalf("status %s (error %q), want done %v", j.Status, j.Error, c.done)
			}
			if c.done {
				return
			}
			if !strings.Contains(j.Error, c.words) {
				t.Errorf("the refusal does not name the encoder's depth: %q", j.Error)
			}
			s.assertOriginalSafe()
		})
	}
}

// A card that fails and the processor that retries make another depth for the same 10-bit source: the
// file is judged by the processor's picture, not the card's, so a good file is not kept.
func TestCheckJudgesTheProcessorAfterAFallBack(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx264": true})
	defer rules.SetAvailableFormats(nil)
	s := newScenario(t)
	s.srcSpec = hdSpec("hevc", "yuv420p10le", 1920, 1080)
	s.outSpec = hdSpec("h264", "yuv420p10le", 1920, 1080) // libx264 keeps the source's 10 bits
	s.failEncoder = "h264_qsv"
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return hdFlow("h264", "keep"), 0, nil }
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "h264_qsv", "" }
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: the processor's 10-bit file is right", j.Status, j.Error)
	}
	if !strings.Contains(j.Plan, `"Encoder":""`) { // the record says what judged the file
		t.Errorf("the recorded plan still names the card: %s", j.Plan)
	}
}
