// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplaceOS(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fs.ErrExist}
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EOPNOTSUPP):
		return linkRename(src, dst) // a filesystem without renameat2
	}
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
}
