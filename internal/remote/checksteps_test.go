// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"errors"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Q1 (remote SSIM): the picture check's score travels in the report as the job's check step.
func TestCheckStepCarriesTheScore(t *testing.T) {
	if got := checkSteps(time.Now(), nil, nil); len(got) != 0 {
		t.Fatalf("no check ran, no step: %+v", got)
	}
	score := &worker.CheckScore{Score: 0.91, Min: 0.95, Passed: false}
	steps := checkSteps(time.Now(), score, errors.New("below"))
	if len(steps) != 1 {
		t.Fatalf("one check step, got %+v", steps)
	}
	s := steps[0]
	if s.Index != 1 || s.Name != "check" || s.Outcome != "failed" || s.CheckScore == nil || *s.CheckScore != 0.91 ||
		s.CheckMin == nil || *s.CheckMin != 0.95 || s.CheckPassed == nil || *s.CheckPassed {
		t.Errorf("step: %+v", s)
	}
	ok := checkSteps(time.Now(), &worker.CheckScore{Score: 0.99, Min: 0.95, Passed: true}, nil)
	if len(ok) != 1 || ok[0].Outcome != "ok" || *ok[0].CheckPassed != true {
		t.Errorf("passing check: %+v", ok)
	}
}
