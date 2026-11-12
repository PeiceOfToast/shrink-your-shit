// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !windows

package scanner

import (
	"os"
	"strconv"
	"syscall"
)

// rootDisk is the id of the disk (filesystem) folder p is on (false: it can't be read).
func rootDisk(p string) (string, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(uint64(s.Dev), 10), true
}
