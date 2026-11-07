// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package worker

import (
	"os"
	"syscall"
)

// openNoFollow opens path for reading, refusing a link (O_NOFOLLOW): a file replaced by a link after
// its Lstat is not followed either (slice 23).
func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil && (errorIs(err, syscall.ELOOP) || errorIs(err, syscall.EMLINK)) { // EMLINK: FreeBSD's answer
		return nil, errIsLink
	}
	return f, err
}
