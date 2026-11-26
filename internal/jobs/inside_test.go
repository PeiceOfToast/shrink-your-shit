// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
)

// A folder differing from a library only in letter case is inside it: macOS and SMB or NAS mounts compare
// names case-insensitively, so a miss would convert the files twice (#766 follow-up).
func TestOutputInsideIgnoresCase(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir()) // macOS: the temp folder is under a link
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, dir, lib string
		want           bool
	}{
		{"inside, other case", filepath.Join(tmp, "films", "out"), filepath.Join(tmp, "Films"), true},
		{"same folder, other case", filepath.Join(tmp, "FILMS"), filepath.Join(tmp, "films"), true},
		{"sibling with a longer name", filepath.Join(tmp, "Films2"), filepath.Join(tmp, "films"), false},
		{"default folder beside the library", filepath.Join(tmp, "Films (shrunk)"), filepath.Join(tmp, "films"), false},
	}
	for _, c := range cases {
		if got := OutputInside(c.dir, c.lib); got != c.want {
			t.Errorf("%s: OutputInside(%q, %q) = %v, want %v", c.name, c.dir, c.lib, got, c.want)
		}
	}
}
