// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package fsx

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// FreeBytes is the space available to this user on the volume that holds path. A folder on a full
// disk answers a small number, not an error.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("read the free space of %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
