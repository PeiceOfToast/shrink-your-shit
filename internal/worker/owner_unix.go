// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package worker

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// keepOwner gives path the owner and group of before. Only an administrator may give a file to someone
// else, so without that right a file made by this user stays this user's: not an error. The group alone
// can still be kept when this user is in it.
func keepOwner(path string, before os.FileInfo) error {
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	err := os.Lchown(path, int(st.Uid), int(st.Gid))
	if errors.Is(err, fs.ErrPermission) {
		if err := os.Lchown(path, -1, int(st.Gid)); errors.Is(err, fs.ErrPermission) {
			return nil
		} else {
			return err
		}
	}
	return err
}
