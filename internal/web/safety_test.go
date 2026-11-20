// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// FB2b: "Workflows may run commands" is off by default and the policy follows it.
func TestWorkflowsMayRunCommandsDefaultsOff(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := LoadSettings(st)
	if err != nil || s.WorkflowsMayRunCommands || s.Policy().CommandsAllowed || policyFor(st).CommandsAllowed {
		t.Fatalf("default: %+v %v", s, err)
	}
	s.WorkflowsMayRunCommands = true
	if err := (&settingsStore{store: st}).save(s); err != nil {
		t.Fatal(err)
	}
	if !policyFor(st).CommandsAllowed {
		t.Error("saved on: the policy should allow commands")
	}
}
