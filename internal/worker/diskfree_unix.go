// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package worker

import "golang.org/x/sys/unix"

// diskFree returns the bytes a normal user can still write on the volume that holds dir.
func diskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// diskSize is the size of the disk dir is on (S6: the 5% rule for a folder for new files).
func diskSize(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), nil
}
