// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package fsx

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// FreeBytes is the space available to this user on the volume that holds path. A folder on a full
// disk answers a small number, not an error.
func FreeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("read the free space of %s: %w", path, err)
	}
	return free, nil
}
