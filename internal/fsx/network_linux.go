// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package fsx

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// The file-system magic numbers that mean a network share (linux/magic.h). The kernel reports CIFS
// mounts, SMB1 and SMB2/3 alike, under these types. They are uint32 because statfs reports them as
// int32 on 32-bit targets, which cannot hold 0xFF534D42 as a positive number.
const (
	cifsMagic uint32 = 0xFF534D42
	smb2Magic uint32 = 0xFE534D42
)

// IsNetwork reports whether path is on a network share (NFS, SMB/CIFS), and names the kind for the
// sentence that refuses it (K15: SQLite does not survive WAL over a network share). The name is
// empty when it is not.
func IsNetwork(path string) (bool, string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, "", fmt.Errorf("find the file system of %s: %w", path, err)
	}
	switch uint32(st.Type) { // the bits are the same at every width
	case unix.NFS_SUPER_MAGIC:
		return true, "NFS", nil
	case cifsMagic, smb2Magic, unix.SMB_SUPER_MAGIC:
		return true, "SMB", nil
	}
	return false, "", nil
}
