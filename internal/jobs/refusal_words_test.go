// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

// Each refusal kind says what it found in its own words, with the first reason only (slice 14).
func TestRefusalWordsSayWhy(t *testing.T) {
	for _, c := range []struct{ code, stored, message, hint string }{
		{"check_differs", "the new file is 320 wide but the plan says 640; the audio is missing",
			"The new file isn't what the workflow asked for: the new file is 320 wide but the plan says 640. The original is untouched.",
			"Open the Check step in the Log to see what differs. Retrying gives the same result. If the workflow asks for something this encoder can't make, change its Convert step."},
		{"output_damaged", "the new file is empty.", "The new file came out damaged: the new file is empty. The original is untouched.",
			"Retry. If it happens again, convert this file on the processor."},
		{"check_not_run", "the picture comparison at 0:00:00 gave no result",
			"A check couldn't run, so the original is kept: the picture comparison at 0:00:00 gave no result.",
			"Retry later. If it keeps happening, Settings > About says what is wrong."},
	} {
		msg, hint := FailureWords(c.code, FailureFacts{ExitCode: -1, Stored: c.stored})
		if msg != c.message || hint != c.hint {
			t.Errorf("%s:\n got %q / %q\nwant %q / %q", c.code, msg, hint, c.message, c.hint)
		}
	}
	// job 48's shape: a different width is about the file's shape, so its next step says nothing about quality
	if _, hint := FailureWords("check_differs", FailureFacts{ExitCode: -1, Stored: "the picture is 320 wide"}); strings.Contains(strings.ToLower(hint), "quality") {
		t.Errorf("check_differs: the next step mentions quality: %q", hint)
	}
}
