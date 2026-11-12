// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package scanner

import (
	"os"
	"path/filepath"
	"strings"
)

// rootDisk is the drive or share folder p is on (false: it can't be read).
func rootDisk(p string) (string, bool) {
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return strings.ToLower(filepath.VolumeName(p)), true
}
