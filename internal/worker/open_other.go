// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix

package worker

import (
	"io/fs"
	"os"
)

// openNoFollow opens path for reading, refusing a link. Windows has no O_NOFOLLOW: a symbolic link or
// any other reparse point (a junction) is seen by Lstat, checked right before opening.
func openNoFollow(path string) (*os.File, error) {
	lst, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if lst.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return nil, errIsLink
	}
	return os.Open(path)
}
