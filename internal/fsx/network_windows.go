// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package fsx

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// DRIVE_REMOTE is GetDriveType's answer for a network drive (winbase.h).
const driveRemote = 4

// IsNetwork reports whether path is on a network share, and names the kind for the sentence that
// refuses it (K15). A UNC path (\\server\share) is one; so is any drive whose type is DRIVE_REMOTE.
// The name is empty when it is not a network share.
func IsNetwork(path string) (bool, string, error) {
	if strings.HasPrefix(path, `\\`) {
		return true, "network share", nil
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, "", fmt.Errorf("find the drive of %s: %w", path, err)
	}
	if windows.GetDriveType(p) == driveRemote {
		return true, "network share", nil
	}
	return false, "", nil
}
