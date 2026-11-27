// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package fsx

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DRIVE_REMOTE is GetDriveType's answer for a network drive (winbase.h).
const driveRemote = 4

// IsNetwork reports whether path is on a network share, and names the kind for the sentence that
// refuses it (K15). The path is made absolute first, because GetDriveType is documented for a
// volume root and a relative path can sit on a mapped drive. The name is empty when it is not a
// network share.
func IsNetwork(path string) (bool, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, "", fmt.Errorf("find the absolute path of %s: %w", path, err)
	}
	if kind, askDrive := networkPathKind(abs); !askDrive {
		return kind != "", kind, nil
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(abs) + `\`)
	if err != nil {
		return false, "", fmt.Errorf("find the drive of %s: %w", path, err)
	}
	if windows.GetDriveType(root) == driveRemote {
		return true, "network share", nil
	}
	return false, "", nil
}
