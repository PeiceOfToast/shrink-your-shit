// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func renameNoReplaceOS(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, 0) // no MOVEFILE_REPLACE_EXISTING: fails if dst exists
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fs.ErrExist}
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}
