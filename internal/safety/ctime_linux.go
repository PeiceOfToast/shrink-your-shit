// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"os"
	"syscall"
)

// ctimeOf is the inode change time, which an edit that keeps size and mtime still moves (L-SAFE-5).
func ctimeOf(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ctim.Nano()
	}
	return 0
}
