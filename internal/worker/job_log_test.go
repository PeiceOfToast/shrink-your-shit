// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-13 item E: a job's encode step (its command, time and ffmpeg lines) and, when it fails, the
// failure summary are in the database, so the Jobs list can say why without the log file.
func TestEncodeStepAndFailureAreInTheDatabase(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
	s.encodeErr = errors.New("exit status 1")
	s.encodeStderr = "Input #0, matroska\nInvalid data found when processing input\nlast line"
	w.RunOne(context.Background())
	steps, _ := s.store.RunSteps(s.id)
	if len(steps) != 1 || steps[0].Name != "encode" || steps[0].Outcome != "failed" || !strings.HasPrefix(steps[0].Command, "ffmpeg ") {
		t.Fatalf("steps %+v", steps)
	}
	f, _ := s.store.FailureOf(s.id)
	if f == nil || len(f.KeyLines) == 0 || !strings.Contains(strings.Join(f.KeyLines, "|"), "Invalid data found") {
		t.Fatalf("failure %+v", f)
	}
	lines, _ := s.store.LogLines(s.id)
	var stderr int
	for _, l := range lines {
		if l.Stream == "stderr" {
			stderr++
		}
	}
	if stderr != 3 {
		t.Errorf("stderr lines %d: %+v", stderr, lines)
	}
}
