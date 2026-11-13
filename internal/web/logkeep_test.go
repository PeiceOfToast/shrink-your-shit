// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// User review 11-13 item E: logKeepDays is 7, 30, 90 or 0 (forever); the default is 30.
func TestLogKeepDays(t *testing.T) {
	if d := defaultSettings(); d.LogKeepDays != 30 {
		t.Errorf("default %d", d.LogKeepDays)
	}
	for _, days := range []int{0, 7, 30, 90} {
		s := defaultSettings()
		s.LogKeepDays = days
		if code, msg := s.problem(); code != "" {
			t.Errorf("%d days refused: %s", days, msg)
		}
	}
	for _, days := range []int{1, 14, 365, -1} {
		s := defaultSettings()
		s.LogKeepDays = days
		if code, _ := s.problem(); code == "" {
			t.Errorf("%d days accepted", days)
		}
	}
}

// A finished job whose lines were pruned (its steps remain) is expired; one with neither is none.
func TestLogStateExpired(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.RecordRunStep(5, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok"})
	if got := logState(st, 5, jobs.Done, ""); got != "expired" {
		t.Errorf("pruned: %s", got)
	}
	if got := logState(st, 6, jobs.Done, ""); got != "none" {
		t.Errorf("no log at all: %s", got)
	}
}
