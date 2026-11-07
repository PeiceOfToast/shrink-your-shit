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
	if s2.count("-crf 26 -preset medium") != 1 { // 27 at 1080p, one lower for this 360p source
		t.Errorf("Smallest encodes a 360p file at crf 26, medium:\n%s", strings.Join(s2.calls, "\n"))
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

// The job keeps the Plan it ran with, the Expect it was checked against and the settings that judged
// it, with their fingerprint.
func TestJobRecordsItsPlan(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(4), nil }
	w.RunOne(context.Background())
	j := s.job()
	var snap rules.Snapshot
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatalf("plan %q: %v", j.Plan, err)
	}
	p := snap.Plan
	if p.Kind != rules.KindSlider || p.Video.CRF != 20 || p.MinSavingPct != 8 || p.MinSSIM != 0.965 || p.Eligibility.FromCodec != "h264" {
		t.Errorf("recorded plan %+v", p)
	}
	if len(snap.Expect.Streams) != 4 || snap.Expect.MinSavings != 0.08 || snap.Verify.MinSSIM != DefaultMinSSIM || snap.Verify.DecodeCheck != "auto" {
		t.Errorf("recorded expect/verify %+v %+v", snap.Expect, snap.Verify)
	}
	if j.PlanPrint != snap.Fingerprint() {
		t.Errorf("fingerprint %q, want %q", j.PlanPrint, snap.Fingerprint())
	}
	if s.count("-crf 20 -preset medium") != 1 { // Better quality: 21 at 1080p, 20 for this 360p source
		t.Errorf("the encode must run the recorded plan:\n%s", strings.Join(s.calls, "\n"))
	}
}

// failingRecord is the store with RecordPlan failing, like a database write error.
type failingRecord struct{ Queue }

func (failingRecord) RecordPlan(int64, string, string) error { return errors.New("disk I/O error") }

// If the job's Plan cannot be recorded, nothing is converted: the job fails before any file is written.
func TestJobStopsIfItsPlanCannotBeRecorded(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Queue = failingRecord{s.store}
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "could not record what this conversion will do") || !strings.Contains(j.Error, "original is untouched") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if s.count("libx265") != 0 {
		t.Error("ffmpeg must not run")
	}
	s.assertOriginalSafe()
}
