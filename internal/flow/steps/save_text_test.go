// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps_test

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// saveLine is the save step's card line for a flow, checked with keepDays as the env's setting.
func saveLine(t *testing.T, f flow.Flow, env flow.Env) string {
	t.Helper()
	for _, ref := range f.Steps {
		if ref.Type != "file.save" {
			continue
		}
		_, settings, err := flow.DecodeStep(ref)
		if err != nil {
			t.Fatal(err)
		}
		return steps.SaveStep{}.Validate(settings, flow.ValidateContext{Flow: f, Env: env}).Summary
	}
	t.Fatal("the starter has no file.save step")
	return ""
}

// The replace line names the setting the run reads, and a day count of one is singular.
func TestSaveTextNamesTheSettingsTrashDays(t *testing.T) {
	f := setStep(t, starter(t), "file.save", "where", "replace")
	if got := saveLine(t, f, flow.Env{KeepDays: func() int { return 30 }}); !strings.Contains(got, "old file in the trash 30 days") {
		t.Errorf("replace at 30 = %q", got)
	}
	if got := saveLine(t, f, flow.Env{KeepDays: func() int { return 1 }}); !strings.Contains(got, "old file in the trash 1 day") {
		t.Errorf("replace at 1 = %q", got)
	}
}

// With no setting read, the line uses the default (7 days), never a fixed number from the code.
func TestSaveTextWithoutASettingUsesTheDefault(t *testing.T) {
	f := setStep(t, starter(t), "file.save", "where", "replace")
	if got := saveLine(t, f, flow.Env{}); !strings.Contains(got, "old file in the trash 7 days") {
		t.Errorf("replace with no setting = %q, want the default 7 days", got)
	}
}

// The text follows the Save mode for the other two modes, and is unchanged for next-to.
func TestSaveTextNextTo(t *testing.T) {
	f := setStep(t, starter(t), "file.save", "where", "next_to")
	if got := saveLine(t, f, flow.Env{KeepDays: func() int { return 30 }}); !strings.Contains(got, "next to the original") {
		t.Errorf("next_to = %q", got)
	}
}
