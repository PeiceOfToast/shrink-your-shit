// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"syscall"
	"time"
)

// ChangeTime is the file's status change time (ctime), which every write, rename or
// timestamp change updates, and which a program cannot set back.
func ChangeTime(fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Ctim.Sec, st.Ctim.Nsec), true
}
