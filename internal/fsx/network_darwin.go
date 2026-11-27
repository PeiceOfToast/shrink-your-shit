// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build darwin

package fsx

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// IsNetwork reports whether path is on a network share (NFS, SMB/CIFS, AFP), and names the kind for
// the sentence that refuses it (K15). The name is empty when it is not.
func IsNetwork(path string) (bool, string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, "", fmt.Errorf("find the file system of %s: %w", path, err)
	}
	switch cString(st.Fstypename[:]) {
	case "nfs":
		return true, "NFS", nil
	case "smbfs":
		return true, "SMB", nil
	case "afpfs":
		return true, "AFP", nil
	}
	return false, "", nil
}

// cString is a NUL-terminated statfs name as a Go string.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
