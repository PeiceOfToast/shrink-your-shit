// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A retried job keeps its earlier try's steps; while it converts again it reads running with its
// percent, not committing from the old try's verified step (CT 124, 11-18: the dashboard's bar flickered).
func TestV2StateUsesOnlyThisAttempt(t *testing.T) {
	pct := 36.0
	old := []jobs.Step{{Step: "found", OK: true}, {Step: "checked", OK: true}, {Step: "converted", OK: true}, {Step: "verified", OK: false}}
	if got := v2State(string(jobs.Running), old, &pct); got != "running" {
		t.Fatalf("retried job converting: state %q, want running", got)
	}
	if got := v2State(string(jobs.Running), old, nil); got != "preparing" {
		t.Fatalf("retried job before ffmpeg starts: state %q, want preparing", got)
	}
	again := append(append([]jobs.Step{}, old...), jobs.Step{Step: "converted", OK: true})
	if got := v2State(string(jobs.Running), again, nil); got != "validating" {
		t.Fatalf("retried job converted again: state %q, want validating", got)
	}
	first := []jobs.Step{{Step: "found", OK: true}, {Step: "converted", OK: true}, {Step: "verified", OK: true}}
	if got := v2State(string(jobs.Running), first, nil); got != "committing" {
		t.Fatalf("first try verified: state %q, want committing", got)
	}
}
