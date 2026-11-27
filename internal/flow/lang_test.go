// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// TestCanonLang is L-AUD-16: lowercase, the language table's 639-2/B code, "" for unset, und and
// unknown, "zxx" kept as itself.
func TestCanonLang(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"und":   "",
		"UND":   "",
		"eng":   "eng",
		"EN":    "eng",
		"deu":   "ger", // 639-2/T
		"ger":   "ger",
		"de":    "ger", // two-letter
		"fra":   "fre",
		"fr":    "fre",
		"zho":   "chi",
		"zh":    "chi",
		"chi":   "chi",
		"zxx":   "zxx",
		" yue ": "",
		"nope":  "",
	}
	for in, want := range cases {
		if got := flow.CanonLang(in); got != want {
			t.Errorf("CanonLang(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSameLanguage is L-AUD-17: und and "" are the same, and case and spelling do not matter.
func TestSameLanguage(t *testing.T) {
	if !flow.SameLanguage("und", "") || !flow.SameLanguage("", "") {
		t.Error("und and empty must compare equal")
	}
	if !flow.SameLanguage("deu", "ger") || !flow.SameLanguage("en", "ENG") {
		t.Error("spellings of one language must compare equal")
	}
	if flow.SameLanguage("eng", "ger") || flow.SameLanguage("und", "eng") {
		t.Error("different languages must not compare equal")
	}
}
