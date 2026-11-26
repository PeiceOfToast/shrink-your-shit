// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"strings"
)

// Inside reports whether path is dir itself or inside it. Letters are compared folded on every OS: macOS and
// SMB or NAS mounts are case-insensitive, so "Films/out" is inside "films", and a miss converts files twice
// (#766 follow-up). A rare false refusal between two folders differing only in case is visible and fixable.
func Inside(path, dir string) bool {
	rel, err := filepath.Rel(strings.ToLower(dir), strings.ToLower(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolved is path with its links followed, as far as it exists (a folder for new files may not exist
// yet: its existing parent is resolved and the rest added back).
func Resolved(path string) string {
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(p) == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// OutputInside reports whether new files saved to dir would land inside library folder lib, or around
// it (links followed, so an alias can't hide it, review 6cafc6f): they would be converted again.
func OutputInside(dir, lib string) bool {
	real, lr := Resolved(filepath.Clean(dir)), Resolved(filepath.Clean(lib))
	return Inside(real, lr) || Inside(lr, real)
}
