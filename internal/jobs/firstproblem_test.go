// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "testing"

// A refusal's stored words can carry the next step as a sentence ("Press Retry later; …"). firstProblem keeps only
// the problem, so design's sentence names what went wrong and the hint says the rest.
func TestFirstProblemStopsBeforeTheNextStep(t *testing.T) {
	for _, c := range []struct{ stored, want string }{
		{"reading the new file took too long. Press Retry later; a very slow disk or a damaged file can cause this",
			"reading the new file took too long"},
		{"reading the new file took too long. Press Retry later", "reading the new file took too long"},
		{"the audio is missing; the picture changed. Retry.", "the audio is missing"},
	} {
		if got := firstProblem(c.stored); got != c.want {
			t.Errorf("firstProblem(%q) = %q, want %q", c.stored, got, c.want)
		}
	}
}
