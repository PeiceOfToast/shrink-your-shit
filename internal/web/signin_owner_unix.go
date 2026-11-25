// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package web

import (
	"os"
	"syscall"
)

// folderOwner gives the uid and gid that own dir, when this process runs as root and the owner is not root:
// the case where a file written by reset-password must be handed to that owner.
func folderOwner(dir string) (uid, gid int, ok bool) {
	if os.Geteuid() != 0 {
		return 0, 0, false
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, 0, false
	}
	st, isUnix := fi.Sys().(*syscall.Stat_t)
	if !isUnix || st.Uid == 0 {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
