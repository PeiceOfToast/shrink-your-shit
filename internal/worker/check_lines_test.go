// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// 12c: the job log says when the playback and picture checks start and how long each took, in that order.
func TestCheckStartAndEndLinesAreInOrder(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.99 U:0.99 V:0.99 All:0.990000 (20.0)"
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
	w.RunOne(context.Background())
	lines, _ := s.store.LogLines(s.id)
	want := []string{"Playing the new file to check it (about ", "Played in ",
		"Comparing picture quality with the original.", "Compared in "}
	at := -1
	for _, prefix := range want {
		found := -1
		for i, l := range lines {
			if i > at && strings.HasPrefix(l.Text, prefix) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("no log line %q after line %d; log: %v", prefix, at, texts(lines))
		}
		at = found
	}
}

func texts(ls []jobs.LogLine) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Text)
	}
	return out
}
