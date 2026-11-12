// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package jobs

import (
	"path/filepath"
	"strings"
)

// sameDiskOS: the same drive or share (a folder mounted into another drive is not told apart).
func sameDiskOS(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}
