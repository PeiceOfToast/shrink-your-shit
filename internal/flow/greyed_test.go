// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// TestGreyedAnswerFollowsTheGuide: every choice the guide greys is refused with the guide's own
// reason, and every choice it offers is not. The flags are read from the same guide the chips are
// drawn from, so the two cannot disagree.
func TestGreyedAnswerFollowsTheGuide(t *testing.T) {
	g := flow.Guide()
	checked := 0
	for _, part := range g.Parts {
		for _, f := range part.Fields {
			if f.Type != "choice" {
				continue
			}
			for _, c := range f.Choices {
				if c.Available {
					continue
				}
				checked++
				p, greyed := flow.GreyedAnswer(flow.Answers{f.Key: c.Value}, flow.DefaultAnswers())
				if !greyed {
					t.Errorf("%s=%s is greyed in the guide but not refused", f.Key, c.Value)
					continue
				}
				if p.Field != f.Key || p.Code != "invalid_answer" || p.Message != c.Why {
					t.Errorf("%s=%s = %+v; want invalid_answer on %s with %q", f.Key, c.Value, p, f.Key, c.Why)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the guide greys no choice, so this test proves nothing")
	}
	if _, greyed := flow.GreyedAnswer(flow.DefaultAnswers(), flow.DefaultAnswers()); greyed {
		t.Error("the guide's own defaults are refused as greyed")
	}
}
