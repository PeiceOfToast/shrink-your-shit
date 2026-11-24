// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Convert step's frame-rate limit: keep is the default and writes no key; each limit is written and reads back.
func TestMaxFrameRateSettingRoundTrips(t *testing.T) {
	cv, _ := Definition(TypeConvert)
	props := cv.ConfigSchema["properties"].(map[string]any)
	p, _ := props["maxFrameRate"].(map[string]any)
	if p["default"] != "keep" || p["title"] != "Frame rate" || !strings.Contains(p["description"].(string), "Only ever lowered") {
		t.Fatalf("the Convert step's frame rate: %v", p)
	}
	s := rules.DefaultSelectors(3)
	for _, choice := range []string{"", "keep"} {
		s.MaxFrameRate = choice
		if _, ok := convertConfig(s)["maxFrameRate"]; ok {
			t.Errorf("frame rate %q writes a key", choice)
		}
	}
	for _, choice := range []string{"24", "25", "30", "60"} {
		s.MaxFrameRate = choice
		if got := convertConfig(s)["maxFrameRate"]; got != choice {
			t.Errorf("frame rate %q writes %v", choice, got)
		}
		d := FromTemplate(rules.Template{Kind: rules.KindSelectors, Selectors: &s, CompilerVersion: rules.CompilerVersion}, nil)
		d = roundTrip(t, d)
		if r := Validate(d); r.Errors() || !r.Stackable {
			t.Fatalf("frame rate %q: %+v", choice, r.Issues)
		}
		tm, issues := ToTemplate(d, rules.Template{})
		if len(issues) > 0 || tm.Selectors == nil {
			t.Fatalf("frame rate %q: %+v", choice, issues)
		}
		if tm.Selectors.MaxFrameRate != choice {
			t.Errorf("frame rate %q reads back as %q", choice, tm.Selectors.MaxFrameRate)
		}
	}
}

// The closed row names a limit in the Convert step's summary; keep adds nothing.
func TestMaxFrameRateSummaryWords(t *testing.T) {
	for choice, want := range map[string]string{"": "", "keep": "", "30": " · at most 30 fps"} {
		s := rules.DefaultSelectors(3)
		s.MaxFrameRate = choice
		got := summary(Node{Type: TypeConvert, Enabled: true, Config: convertConfig(s)})
		if want == "" && strings.Contains(got, "fps") || want != "" && !strings.Contains(got, want) {
			t.Errorf("frame rate %q: summary %q, want it to carry %q", choice, got, want)
		}
	}
}
