// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
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
	w.Template = func(j jobs.Job) (rules.Template, error) {
		return rules.LegacyTemplate(rules.Rule{FromCodec: "h264", CRF: 21, Preset: "slow"}, "beside"), nil
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
	// minimum saving: the slider at Smallest needs 20%; a 12-byte result for the 14-byte original
	// saves 14%, so it is refused
	s2 := newScenario(t)
	s2.content = "0123456789ab"
	w2 := s2.worker(Replace)
	w2.Template = func(j jobs.Job) (rules.Template, error) { return rules.SliderTemplate(1), nil }
	w2.RunOne(context.Background())
	s2.assertKept("saves less than the required 20%")
	if s2.count("-crf 27 -preset medium") != 1 {
		t.Errorf("Smallest encodes at crf 27, medium:\n%s", strings.Join(s2.calls, "\n"))
	}
}

func TestJobOfARemovedLibraryFails(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) {
		return rules.Template{}, errors.New("its library no longer exists")
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

// The job keeps the Plan it ran with, and its settings' fingerprint.
func TestJobRecordsItsPlan(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(4), nil }
	w.RunOne(context.Background())
	j := s.job()
	var p rules.Plan
	if err := json.Unmarshal([]byte(j.Plan), &p); err != nil {
		t.Fatalf("plan %q: %v", j.Plan, err)
	}
	if p.Kind != rules.KindSlider || p.Video.CRF != 21 || p.MinSavingPct != 8 || j.PlanPrint != p.Fingerprint() {
		t.Errorf("recorded plan %+v, fingerprint %q", p, j.PlanPrint)
	}
	if s.count("-crf 21 -preset medium") != 1 {
		t.Errorf("the encode must run the recorded plan:\n%s", strings.Join(s.calls, "\n"))
	}
}
