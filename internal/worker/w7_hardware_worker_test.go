// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// W7 on the fake-ffmpeg scenario: the Convert step's hardware choices decide which encoders the job asks for.
func hardwareWorker(s *scenario, hardware, failMode string) *Worker {
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.Hardware, sel.HardwareFail = hardware, failMode
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	return w
}

// "CPU only": the graphics card is never asked, even when it would take the file.
func TestCPUOnlyNeverAsksForAGraphicsEncoder(t *testing.T) {
	s := newScenario(t)
	w := hardwareWorker(s, "cpu", "")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "libx265") || strings.Contains(enc[0], "hevc_") {
		t.Fatalf("encodes, want the processor alone:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "Processor only, as the Convert step says"); n != 1 {
		t.Errorf("chosen line: %d, want 1", n)
	}
}

// "Hardware only" with no graphics encoder for the file: the job fails in plain words before any encode, and the
// processor never takes it.
func TestHardwareOnlyWithNoGraphicsEncoderFailsPlainly(t *testing.T) {
	s := newScenario(t)
	w := hardwareWorker(s, "only", "")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "", "" }
	w.RunOne(context.Background()) // a failed job returns its error; the status below is the check
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "Graphics card only: no graphics encoder") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	} else if j.ErrorCode != jobs.ReasonHardwareChoice { // the code that gives the drawer design's next step (W7)
		t.Errorf("stored code %q, want %q", j.ErrorCode, jobs.ReasonHardwareChoice)
	}
	if enc := encodeCalls(s); len(enc) != 0 {
		t.Errorf("an encode ran: %v", enc)
	}
	// A refusal before the encode still gets a failure summary, so the drawer has the words and no ffmpeg run.
	sum, err := s.store.FailureOf(s.id)
	if err != nil || sum == nil {
		t.Fatalf("failure summary: %v, %v; want one for the refused job", sum, err)
	}
	if sum.FfmpegRan || len(sum.KeyLines) != 1 || !strings.Contains(sum.KeyLines[0], "Graphics card only: no graphics encoder") {
		t.Errorf("summary %+v, want no ffmpeg run and the refusal words as its key line", sum)
	}
}

// "Hardware only" and the card fails: the job fails with plain words; the processor does not convert the file.
func TestHardwareOnlyFailureFailsTheJob(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_nvenc"
	w := hardwareWorker(s, "only", "")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	w.RunOne(context.Background()) // a failed job returns its error; the status below is the check
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "Graphics card only: the graphics encoder failed") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	} else if j.ErrorCode != jobs.ReasonHardwareChoice {
		t.Errorf("stored code %q, want %q", j.ErrorCode, jobs.ReasonHardwareChoice)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || !strings.Contains(enc[0], "hevc_nvenc") {
		t.Fatalf("encodes, want the card only:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "converted on the processor"); n != 0 {
		t.Errorf("converted-on-processor line: %d, want 0", n)
	}
}

// Failure "fail" with the default "auto": the card's failure is final, not stepped down or retried.
func TestHardwareFailFailsInsteadOfStepping(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_nvenc"
	w := hardwareWorker(s, "auto", "fail")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	w.RunOne(context.Background()) // a failed job returns its error; the status below is the check
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "set to fail rather than convert on the processor") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if enc := encodeCalls(s); len(enc) != 1 {
		t.Errorf("encodes: %d, want 1 (the card only)", len(enc))
	}
}
