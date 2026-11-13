// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"path/filepath"
	"testing"
)

// User review 11-13 item E: lines, steps and the failure summary round-trip through SQLite, in order.
func TestJobLogInSQLite(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AppendLogLine(7, 0, "app", "started"); err != nil {
		t.Fatal(err)
	}
	s.AppendLogLine(7, 1, "stderr", "frame 1")
	s.AppendLogLine(8, 0, "app", "another job")
	lines, _ := s.LogLines(7)
	if len(lines) != 2 || lines[0].Seq != 1 || lines[1].Seq != 2 || lines[1].Text != "frame 1" || lines[1].Step != 1 || lines[1].Stream != "stderr" {
		t.Fatalf("lines %+v", lines)
	}
	score, min, passed := 0.97, 0.95, true
	if err := s.RecordRunStep(7, RunStep{Index: 0, Name: "encode", StartedAt: "t", TookMs: 1200, Outcome: "ok",
		Command: "ffmpeg -i in.mkv out.mkv", CheckScore: &score, CheckMin: &min, CheckPassed: &passed, OutputLines: 1, Result: "done"}); err != nil {
		t.Fatal(err)
	}
	s.RecordRunStep(7, RunStep{Index: 0, Name: "encode", TookMs: 1500, Outcome: "ok"}) // replaces the step
	steps, _ := s.RunSteps(7)
	if len(steps) != 1 || steps[0].TookMs != 1500 || steps[0].CheckScore != nil || steps[0].Command != "" {
		t.Fatalf("steps %+v", steps)
	}
	if f, _ := s.FailureOf(7); f != nil {
		t.Fatalf("no failure yet: %+v", f)
	}
	var many []string
	for i := 0; i < 25; i++ {
		many = append(many, fmt.Sprint("line ", i))
	}
	if err := s.SetFailureSummary(7, FailureSummary{Step: 0, StepCount: 3, AfterMs: 4000, MediaTime: "00:41:12", Hint: "h", ExitCode: 1, KeyLines: many}); err != nil {
		t.Fatal(err)
	}
	f, err := s.FailureOf(7)
	if err != nil || f == nil || f.ExitCode != 1 || f.MediaTime != "00:41:12" || len(f.KeyLines) != 20 || f.KeyLines[19] != "line 24" {
		t.Fatalf("failure %+v %v", f, err)
	}
}

// #214 P2: a failure summary keeps its exit status (-1: none) and whether ffmpeg ran.
func TestFailureSummaryExitAndRan(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetFailureSummary(1, FailureSummary{ExitCode: 3, FfmpegRan: true, KeyLines: []string{"x"}})
	s.SetFailureSummary(2, FailureSummary{ExitCode: -1, FfmpegRan: false})
	if f, _ := s.FailureOf(1); f == nil || f.ExitCode != 3 || !f.FfmpegRan {
		t.Errorf("ran and exited: %+v", f)
	}
	if f, _ := s.FailureOf(2); f == nil || f.ExitCode != -1 || f.FfmpegRan || len(f.KeyLines) != 0 {
		t.Errorf("never ran: %+v", f)
	}
}
