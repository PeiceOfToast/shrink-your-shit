// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A job runs with its library's flow: its CRF and preset, its output mode and its minimum saving.
func TestJobUsesItsLibrarysFlow(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Settings = func(j jobs.Job) (rules.Settings, error) {
		return rules.Settings{Rule: rules.Rule{FromCodec: "h264", CRF: 21, Preset: "slow"}, Output: "beside"}, nil
	}
	w.RunOne(context.Background())
	if s.count("-crf 21 -preset slow") != 1 {
		t.Errorf("the encode must use the flow's crf and preset:\n%s", strings.Join(s.calls, "\n"))
	}
	if b, _ := os.ReadFile(BesidePath(s.src)); string(b) != "new" {
		t.Error("the flow says beside, so the new file goes beside the original")
	}
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Error("beside keeps the original")
	}
	// minimum saving: "new" (3 bytes) against "original-bytes" (14) saves 79%, so 80% refuses it
	s2 := newScenario(t)
	w2 := s2.worker(Replace)
	w2.Settings = func(j jobs.Job) (rules.Settings, error) {
		return rules.Settings{Rule: rules.Default(), Output: "replace", MinSavingPct: 80}, nil
	}
	w2.RunOne(context.Background())
	s2.assertKept("saves less than the required 80%")
}

func TestJobOfARemovedLibraryFails(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Settings = func(jobs.Job) (rules.Settings, error) {
		return rules.Settings{}, errors.New("its library no longer exists")
	}
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "library no longer exists") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if s.count("libx265") != 0 {
		t.Error("nothing may be encoded")
	}
	s.assertOriginalSafe()
}

// StopJob (its library was removed while converting): the original is untouched, nothing is left
// behind, and the job record is dropped rather than requeued.
func TestStopJobDropsTheRunningJob(t *testing.T) {
	s := newScenario(t)
	var encoding atomic.Bool
	s.hang = func(name string, args []string) bool {
		if name == "ffmpeg" && s.has(args, "libx265") {
			encoding.Store(true)
			return true
		}
		return false
	}
	w := s.worker(Replace)
	done := make(chan struct{})
	go func() { w.RunOne(context.Background()); close(done) }()
	for i := 0; !encoding.Load(); i++ {
		if i > 200 {
			t.Fatal("the encode never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.StopJob(s.id + 1) // another job: nothing happens
	w.StopJob(s.id)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopJob did not stop the job")
	}
	if _, err := s.store.Get(s.id); err == nil {
		t.Error("the stopped job must be dropped, not requeued")
	}
	s.assertOriginalSafe()
}
