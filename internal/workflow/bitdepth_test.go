// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Convert step's bit depth: keep is the default, so a flow that keeps the depth writes no key and reads back as
// it was; 8 and 10 are written and read back.
func TestBitDepthSettingRoundTrips(t *testing.T) {
	cv, _ := Definition(TypeConvert)
	props := cv.ConfigSchema["properties"].(map[string]any)
	if p, _ := props["bitDepth"].(map[string]any); p["default"] != "keep" {
		t.Fatalf("the Convert step's bit depth: %v", props["bitDepth"])
	}
	s := rules.DefaultSelectors(3)
	for _, choice := range []string{"", "keep"} {
		s.BitDepth = choice
		if _, ok := convertConfig(s)["bitDepth"]; ok {
			t.Errorf("bit depth %q writes a key: a flow that keeps the depth reads back as it was", choice)
		}
	}
	for _, choice := range []string{"8", "10"} {
		s.BitDepth = choice
		if got := convertConfig(s)["bitDepth"]; got != choice {
			t.Errorf("bit depth %q writes %v", choice, got)
		}
		d := FromTemplate(rules.Template{Kind: rules.KindSelectors, Selectors: &s, CompilerVersion: rules.CompilerVersion}, nil)
		d = roundTrip(t, d)
		if r := Validate(d); r.Errors() || !r.Stackable {
			t.Fatalf("bit depth %q: %+v", choice, r.Issues)
		}
		tm, issues := ToTemplate(d, rules.Template{})
		if len(issues) > 0 || tm.Selectors == nil {
			t.Fatalf("bit depth %q: %+v", choice, issues)
		}
		if tm.Selectors.BitDepth != choice {
			t.Errorf("bit depth %q reads back as %q", choice, tm.Selectors.BitDepth)
		}
	}
}

// The closed row names a non-default depth in the Convert step's summary; keep adds nothing.
func TestBitDepthSummaryWords(t *testing.T) {
	for choice, want := range map[string]string{"": "", "keep": "", "8": " · 8-bit", "10": " · 10-bit"} {
		s := rules.DefaultSelectors(3)
		s.BitDepth = choice
		n := Node{Type: TypeConvert, Enabled: true, Config: convertConfig(s)}
		got := summary(n)
		if want == "" && strings.Contains(got, "-bit") || want != "" && !strings.Contains(got, want) {
			t.Errorf("bit depth %q: summary %q, want it to carry %q", choice, got, want)
		}
	}
}
