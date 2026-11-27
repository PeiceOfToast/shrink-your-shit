// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// TestRunSourceCheckKeepsADamagedOriginal: the original is decoded before anything is written
// (L-SAFE-4); damage above the budget keeps it with its reason, and no encoder ever runs.
func TestRunSourceCheckKeepsADamagedOriginal(t *testing.T) {
	root, src, info := libraryFile(t, "damaged.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	message := "The original file is damaged (14 damaged pictures of 500, 2.80%), so it is kept as it is and not converted."
	sf := &fakeSafety{source: safety.Verdict{
		OK: false, Code: safety.CodeOriginalDamaged, Message: message, NextAction: safety.NextKeepOriginal,
	}}
	env := newEnv(m, sf, &recorder{}, starterFlow(t), nil)
	env.SafetyLog = safety.Recorder{JobID: 42}
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != safety.CodeOriginalDamaged {
		t.Fatalf("result = %+v, want bigger_kept %s", res, safety.CodeOriginalDamaged)
	}
	if res.Message != message {
		t.Errorf("message = %q, want the check's sentence", res.Message)
	}
	if got := len(m.Argvs()); got != 0 {
		t.Errorf("%d encoder runs; a damaged original must never be converted", got)
	}
	if len(sf.staged) != 0 {
		t.Errorf("staged %d times; nothing may be written", len(sf.staged))
	}
	if sf.sourceCalls != 1 || sf.gotSource.Path != src {
		t.Errorf("source check calls = %d for %q; want one for the original", sf.sourceCalls, sf.gotSource.Path)
	}
	if sf.gotRecorder.JobID != 42 {
		t.Errorf("recorder job = %d; want the run's job so the check is in the job log", sf.gotRecorder.JobID)
	}
}

// TestRunSourceCheckUnavailableIsRetried: a check that could not run is not a "no" (L-SAFE-1); the
// job retries and nothing is converted.
func TestRunSourceCheckUnavailableIsRetried(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{sourceE: errors.New("ffmpeg: not found")}
	res := flow.Run(context.Background(), runInput(t, root, src), newEnv(m, sf, &recorder{}, starterFlow(t), nil))

	if res.Outcome != flow.OutcomeMayPass || res.Reason != "check.unavailable" {
		t.Fatalf("result = %+v, want may_pass check.unavailable", res)
	}
	if got := len(m.Argvs()); got != 0 {
		t.Errorf("%d encoder runs; nothing is converted when the original could not be checked", got)
	}
}
