// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// lastStep runs the job once with w and returns its final Timeline step's name and words.
func lastStep(t *testing.T, s *scenario, w *Worker) (string, string) {
	t.Helper()
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("run: %v, status %s %q", err, s.job().Status, s.job().Error)
	}
	steps, err := s.store.Steps(s.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	last := steps[len(steps)-1]
	return last.Step, last.Words
}

// templateWorker is a worker with no Mode of its own (the default) whose library flow saves with after.
func templateWorker(s *scenario, after string) *Worker {
	sel := rules.DefaultSelectors(3)
	sel.AfterConverting = after
	if err := sel.Validate(); err != nil {
		s.t.Fatal(err)
	}
	tmpl := sel.Template()
	w := s.worker("")
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	return w
}

// A beside conversion ends "kept" even when the worker's own Mode is the default: the step follows the job's plan.
func TestBesideLastStepFollowsThePlanNotTheWorkerMode(t *testing.T) {
	s := newScenario(t)
	name, words := lastStep(t, s, templateWorker(s, "beside"))
	if name != "kept" || !strings.Contains(words, "written next to the original") {
		t.Errorf("last step %q %q, want kept with the beside sentence", name, words)
	}
}

// An in-place conversion still ends "replaced" on a worker whose Mode is the default.
func TestInPlaceLastStepFollowsThePlan(t *testing.T) {
	s := newScenario(t)
	name, words := lastStep(t, s, templateWorker(s, "replace"))
	if name != "replaced" || !strings.Contains(words, "The new file is in place") {
		t.Errorf("last step %q %q, want replaced", name, words)
	}
}

// A folder of its own ends "kept" with the folder sentence, as before.
func TestFolderLastStepSaysWhereItWent(t *testing.T) {
	s, w, _ := policyCase(t, true)
	name, words := lastStep(t, s, w)
	if name != "kept" || !strings.Contains(words, "The new file was written to ") {
		t.Errorf("last step %q %q, want kept with the folder sentence", name, words)
	}
}
