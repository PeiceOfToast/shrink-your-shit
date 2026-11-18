// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !windows

package jobs

import (
	"os"
	"strconv"
	"syscall"
)

// sameDiskOS: the same device number (a mount point is another device).
func sameDiskOS(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return false
	}
	da, ok1 := sa.Sys().(*syscall.Stat_t)
	db, ok2 := sb.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && da.Dev == db.Dev
}

// diskOf is the id of the disk (filesystem) folder p is on (false: it can't be read).
func diskOf(p string) (string, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	d, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(uint64(d.Dev), 10), true
}
