// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Slice 11b follow-up: when the partial output can't be cleared, the failed rung's error lines still come back with
// the error, so the remote failure report and the job log show what ffmpeg said.
func TestTruncateFailureKeepsTheErrorLines(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "b.mkv")
	plan := attemptPlan()
	args, ok := plan.FFmpegArgsWith("a.mkv", tmp, "hevc_qsv")
	if !ok {
		t.Fatal("no hybrid args for hevc_qsv")
	}
	// The rung fails with a line that steps down, then removes its own temp file, so the truncate fails.
	run := func(context.Context, []string) (string, error) {
		os.Remove(tmp)
		return "Unknown encoder 'hevc_qsv'", errors.New("exit status 1")
	}
	res := Attempt(context.Background(), AttemptInput{Plan: plan, SrcPath: "a.mkv", Tmp: tmp,
		First: Rung{Args: args, Encoder: "hevc_qsv"}}, run, AttemptHooks{})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "clearing the partial output") {
		t.Fatalf("Err %v, want the clearing error", res.Err)
	}
	if !strings.Contains(res.Errors, "Unknown encoder 'hevc_qsv'") {
		t.Errorf("Errors %q, want the failed rung's error line kept", res.Errors)
	}
}
