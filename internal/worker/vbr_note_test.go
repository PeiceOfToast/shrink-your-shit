// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// runVBRJob converts one file as a VBR with a max of maxKbps, and returns the job's log lines (the text only).
func runVBRJob(t *testing.T, maxKbps int) []string {
	t.Helper()
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.99 U:0.99 V:0.99 All:0.990000 (20.0)" // the picture is kept: the job converts
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.Resolution, sel.MaxBitrate, sel.RateControl, sel.VBRMaxKbps, sel.Audio, sel.OnlyMyLanguages = "720", "medium", "vbr_legacy", maxKbps, "as_is", false
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	w.RunOne(context.Background())
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

// Review P3 for #358: a file held at its max all the way through says so in the run log.
func TestVBRRunLogSaysHeldAtTheMax(t *testing.T) {
	const want = "Average and max bitrate are both 1,000 kbps, so the file runs at the max throughout."
	found := false
	for _, l := range runVBRJob(t, 1000) {
		if strings.Contains(l, "Average and max bitrate") {
			found = true
			if l != want {
				t.Errorf("run log line %q, want %q", l, want)
			}
		}
	}
	if !found {
		t.Errorf("a file held at its 1,000 kbps max has no run log line about it")
	}
}

// And a file that is not held at the max says nothing about it.
func TestVBRRunLogSaysNothingWhenNotHeldAtTheMax(t *testing.T) {
	for _, l := range runVBRJob(t, 100000) {
		if strings.Contains(l, "Average and max bitrate") {
			t.Errorf("a file under its max got the line: %q", l)
		}
	}
}
