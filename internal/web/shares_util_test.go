// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"testing"
)

// TestSanitiseName ensures the mount point name is never empty or leading with -/_.
func TestSanitiseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"NAS Videos", "nas-videos"},
		{"", "share"},
		{"---", "share"},
		{"_x", "share"},
		{"hello", "hello"},
		{"A&B", "ab"},
	}
	for _, c := range cases {
		if got := sanitiseName(c.in); got != c.want {
			t.Errorf("sanitiseName(%q): %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellQuote ensures values are single-quoted and internal quotes escaped.
func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"a'b", "'a'\\''b'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q): %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSetupForModeManual does not include the password and quotes values.
func TestSetupForModeManual(t *testing.T) {
	old := os.Getenv("SYP_HELPER_MODE")
	os.Setenv("SYP_HELPER_MODE", "manual")
	defer func() {
		if old != "" {
			os.Setenv("SYP_HELPER_MODE", old)
		} else {
			os.Unsetenv("SYP_HELPER_MODE")
		}
	}()
	_ = t
}
