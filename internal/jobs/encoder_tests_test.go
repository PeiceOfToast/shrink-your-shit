// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"testing"
)

// Q1: a computer's encoder test runs are kept in the database, newest first, bounded to encoderTestRuns.
func TestEncoderTestRunsAreKeptAndBounded(t *testing.T) {
	s := open(t)
	run := func(at string, passed bool) []EncoderTest {
		return []EncoderTest{{Encoder: "hevc_nvenc", Passed: passed, Detail: "10-bit"}, {Encoder: "libx265", Passed: true}}
	}
	if err := s.RecordEncoderTests("server", "2026-11-15T10:00:00Z", run("", true)); err != nil {
		t.Fatal(err)
	}
	// the same run again (a worker reports it on every heartbeat) changes nothing
	if err := s.RecordEncoderTests("server", "2026-11-15T10:00:00Z", []EncoderTest{{Encoder: "h264_qsv"}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.EncoderTests("server")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Encoder != "hevc_nvenc" || !got[0].Passed || got[0].Detail != "10-bit" || got[0].TestedAt != "2026-11-15T10:00:00Z" {
		t.Fatalf("one run of two encoders expected, got %+v", got)
	}

	// more runs than are kept: the oldest go, the newest encoderTestRuns stay
	for i := 0; i < encoderTestRuns+3; i++ {
		at := fmt.Sprintf("2026-11-16T%02d:00:00Z", i)
		if err := s.RecordEncoderTests("server", at, run("", false)); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.EncoderTests("server")
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]bool{}
	for _, r := range got {
		runs[r.TestedAt] = true
	}
	if len(runs) != encoderTestRuns || runs["2026-11-15T10:00:00Z"] {
		t.Fatalf("want %d newest runs and the first one pruned, got %d runs (first still kept: %v)", encoderTestRuns, len(runs), runs["2026-11-15T10:00:00Z"])
	}
	if got[0].TestedAt != "2026-11-16T22:00:00Z" {
		t.Fatalf("newest first, got %s", got[0].TestedAt)
	}

	// another computer keeps its own runs
	if err := s.RecordEncoderTests("worker:w1", "2026-11-15T10:00:00Z", run("", true)); err != nil {
		t.Fatal(err)
	}
	other, _ := s.EncoderTests("worker:w1")
	if len(other) != 2 {
		t.Fatalf("worker:w1 has its own run, got %+v", other)
	}
}
