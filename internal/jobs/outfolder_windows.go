// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package jobs

import (
	"os"
	"path/filepath"
	"strings"
)

// sameDiskOS: the same drive or share (a folder mounted into another drive is not told apart).
func sameDiskOS(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

// diskOf is the drive or share folder p is on (false: it can't be read).
func diskOf(p string) (string, bool) {
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return strings.ToLower(filepath.VolumeName(p)), true
}
