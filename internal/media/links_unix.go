// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package media

import (
	"fmt"
	"os"
	"syscall"
)

// LinkCount is how many names the file has (more than 1: it is shared with another folder by a
// hardlink, so converting it frees no space).
func LinkCount(_ string, fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// FileKey identifies the file itself (device and inode), so a file made again under the same name is
// told apart; ok is false when the system does not say.
func FileKey(_ string, fi os.FileInfo) (key string, ok bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Dev, st.Ino), true
	}
	return "", false
}
