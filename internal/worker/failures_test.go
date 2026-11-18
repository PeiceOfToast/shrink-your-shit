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

// onePath is a workflow with one path (quality 5) whose "Convert video" or "Check the result" does f
// when it doesn't work.
func onePath(check bool, f rules.Failure) rules.Template {
	main := rules.SliderTemplate(5)
	fs := &rules.Failures{}
	f.NodeID, f.Name = "conv", "Convert video"
	if check {
		f.NodeID, f.Name = "verify", "Check the result"
		fs.Check = &f
	} else {
		fs.Convert = &f
	}
	return rules.Template{Kind: rules.KindBranches, Branches: &[]rules.Branch{{Then: &main, Path: []string{"in", "conv", "verify", "save"}, OnFailure: fs}}}
}

func lastCRF(s *scenario) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if f := strings.Fields(s.calls[i]); strings.Contains(s.calls[i], "-crf") {
			for j := range f {
				if f[j] == "-crf" && j+1 < len(f) {
					return f[j+1]
				}
			}
		}
	}
	return ""
}

// V6b: when converting doesn't work, the path's choice happens: another path from the original (its own
// settings), leave the file alone, or try again then stop.
func TestFailurePolicies(t *testing.T) {
	broken := errors.New("ffmpeg: exit status 187: Invalid data found") // a lasting failure

	t.Run("fallback", func(t *testing.T) {
		s := newScenario(t)
		other := rules.SliderTemplate(1)
		tm := onePath(false, rules.Failure{Action: "fallback", Fallback: &rules.Branch{Then: &other, Path: []string{"in", "conv2", "verify2", "save2"}}})
		w := s.worker(Replace)
		w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
		s.encodeErr = broken
		w.RunOne(context.Background())
		first := lastCRF(s)
		j := s.job()
		if j.Status != jobs.Queued || j.Attempts != 1 {
			t.Fatalf("after the failure: %s attempts %d %q", j.Status, j.Attempts, j.Error)
		}
		steps, _ := s.store.Steps(j.ID)
		if last := steps[len(steps)-1]; last.Step != "failure" || last.NodeID != "conv" || last.Port != "error" || !strings.Contains(last.Words, "Convert video didn't work:") || !strings.HasSuffix(last.Words, "Trying the other path.") {
			t.Errorf("timeline %+v", last)
		}
		if route, _ := s.store.Route(j.ID); strings.Join(route, ",") != "in,conv2,verify2,save2" {
			t.Errorf("path %v", route)
		}
		s.encodeErr = nil
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("the other path: %s %q", j.Status, j.Error)
		}
		if second := lastCRF(s); second == first || second == "" {
			t.Errorf("the other path's settings: CRF %q, first %q", second, first)
		}
	})

	t.Run("skip", func(t *testing.T) {
		s := newScenario(t)
		tm := onePath(false, rules.Failure{Action: "skip", SkipWords: "Convert video didn't work, so the file was left as it is."})
		w := s.worker(Replace)
		w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
		s.encodeErr = broken
		w.RunOne(context.Background())
		rec, _ := s.store.GetFile(s.src)
		if rec == nil || rec.Reason != rules.SkipLeftByWorkflow || !strings.Contains(rec.Decision, "left as it is") {
			t.Errorf("skipped: %+v", rec)
		}
		s.assertOriginalSafe()
	})

	t.Run("retry then stop", func(t *testing.T) {
		s := newScenario(t)
		tm := onePath(false, rules.Failure{Action: "retry", Max: 2, BackoffSeconds: 60, Then: "stop"})
		w := s.retrying()
		w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
		s.encodeErr = broken
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Queued || j.Attempts != 1 || j.RetryAt == "" {
			t.Fatalf("first try: %s attempts %d at %q", j.Status, j.Attempts, j.RetryAt)
		}
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Failed || j.Attempts != 2 || j.RetryAt != "" {
			t.Errorf("last try: %s attempts %d at %q", j.Status, j.Attempts, j.RetryAt)
		}
	})

	t.Run("check said no: its own choice", func(t *testing.T) {
		s := newScenario(t)
		other := rules.SliderTemplate(1)
		tm := onePath(true, rules.Failure{Action: "fallback", Fallback: &rules.Branch{Then: &other, Path: []string{"in", "conv2"}}})
		w := s.worker(Replace)
		w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
		s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)" // the picture is too different
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Queued {
			t.Errorf("after the check said no: %s %q", j.Status, j.Error)
		}
		steps, _ := s.store.Steps(s.id)
		if last := steps[len(steps)-1]; last.NodeID != "verify" || !strings.HasPrefix(last.Words, "Check the result didn't work:") {
			t.Errorf("timeline %+v", last)
		}
	})
}
