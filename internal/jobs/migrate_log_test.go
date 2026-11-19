// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

// captureLog sends the standard logger to a buffer for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// stepLines are the log lines that report a migration step (not the one-line upgrade summary).
func stepLines(out string) []string {
	var steps []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "migrate: ") {
			steps = append(steps, l)
		}
	}
	return steps
}

// An upgrade logs one line per step it applied, in order, and nothing for the steps it did not run.
func TestUpgradeLogsOneLinePerAppliedStep(t *testing.T) {
	path := legacyDB(t)
	db := rawDB(t, path)
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out := captureLog(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	pending := migrations[1:] // the database was at version 1
	steps := stepLines(out.String())
	if len(steps) != len(pending) {
		t.Fatalf("%d step lines, want %d (one per applied step):\n%s", len(steps), len(pending), out.String())
	}
	for i, m := range pending {
		want := fmt.Sprintf("migrate: %d %s applied in ", m.version, m.name)
		if !strings.Contains(steps[i], want) {
			t.Errorf("step line %d is %q, want it to contain %q", i, steps[i], want)
		}
	}
}

// A database already at the latest version runs no step, so it logs no step line.
func TestOpenWithNothingToDoLogsNoStepLine(t *testing.T) {
	path := legacyDB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	out := captureLog(t)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if steps := stepLines(out.String()); len(steps) != 0 {
		t.Errorf("step lines on an up-to-date database: %q", steps)
	}
}
