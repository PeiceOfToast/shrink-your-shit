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
// else, so without that right a file made by this user stays this user's: the outcome says so (a warning
// in the job log, log-spec R14). The group alone can still be kept when this user is in it.
func keepOwner(path string, before os.FileInfo) (ownerOutcome, error) {
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		return ownerNothing, nil
	}
	err := os.Lchown(path, int(st.Uid), int(st.Gid))
	if err == nil {
		return ownerKept, nil
	}
	if errors.Is(err, fs.ErrPermission) {
		switch gerr := os.Lchown(path, -1, int(st.Gid)); {
		case gerr == nil, errors.Is(gerr, fs.ErrPermission): // the group alone was kept, or neither was
			return ownerRefused, err
		default:
			return ownerFailed, gerr
		}
	}
	return ownerFailed, err
}
