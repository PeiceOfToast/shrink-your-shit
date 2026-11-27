// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"path/filepath"
	"strings"
)

// Inside reports whether child is parent or sits under it, comparing real paths case-insensitively
// on every OS (L-SAFE-15). Both sides are made absolute and their existing part is resolved through
// symlinks, so a folder that does not exist yet still compares by where it would be.
func Inside(child, parent string) bool {
	c := realForCompare(child)
	p := realForCompare(parent)
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false // different volumes on Windows
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// realForCompare is path absolute, symlink-resolved as far as it exists, and lower-cased. The
// nearest existing parent is resolved and the rest appended, because the folder may not exist yet.
func realForCompare(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	rest := ""
	for current := filepath.Clean(abs); ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return strings.ToLower(filepath.Join(real, rest))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return strings.ToLower(filepath.Clean(abs))
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}
