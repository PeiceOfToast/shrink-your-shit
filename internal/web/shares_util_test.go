// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import "testing"

// TestSanitiseName ensures the mount point name is never empty or leading with -/_.
func TestSanitiseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"NAS Videos", "nas-videos"},
		{"", "share-7"},
		{"---", "share-7"},
		{"_x", "share-7"},
		{"hello", "hello"},
		{"A&B", "ab"},
	}
	for _, c := range cases {
		if got := sanitiseName(c.in, 7); got != c.want {
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
