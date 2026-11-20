// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/flow"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
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

// FB2b: a published workflow's "Run a command" card runs while the switch is on and is refused at run time once
// the switch is turned off: the runner is read per job, nil once off, and the card then says why; nothing runs.
func TestCommandCardIsRefusedOnceTheSwitchIsOff(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if CommandRunner(st) != nil {
		t.Fatal("off by default: the runner must be nil")
	}
	s, _ := LoadSettings(st)
	s.WorkflowsMayRunCommands = true
	if err := (&settingsStore{store: st}).save(s); err != nil {
		t.Fatal(err)
	}
	if CommandRunner(st) == nil {
		t.Fatal("switched on: the runner must be there")
	}
	s.WorkflowsMayRunCommands = false
	if err := (&settingsStore{store: st}).save(s); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	card := rules.Card{NodeID: "c", Type: workflow.TypeCommand, Name: "Run a command", Config: map[string]any{"program": os.Args[0], "arguments": "-flow.helper touch " + marker}}
	final := filepath.Join(t.TempDir(), "Film.mkv")
	os.WriteFile(final, []byte("x"), 0o644)
	notes, warnings := flow.After([]rules.Card{card}, &flow.State{Vars: map[string]string{}, Final: final}, flow.Env{Exec: CommandRunner(st)})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "running commands is off") || !notes[0].Failed {
		t.Fatalf("after switch-off: %v %+v", warnings, notes)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the program ran although the switch is off")
	}
}
