// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"errors"
	"fmt"
	"os"
)

// ErrNotRemoved means the link fallback made newpath but could not remove oldpath: the file is at newpath and
// oldpath is left behind.
var ErrNotRemoved = errors.New("fsx: the source could not be removed after linking")

// RenameNoReplace moves oldpath to newpath and fails with an error that matches os.ErrExist when newpath is
// already there. Nothing is ever overwritten (L-SAFE-10). Each OS has its own file: Linux renameat2 with
// RENAME_NOREPLACE, Windows MoveFileEx without the replace flag, and link then unlink elsewhere.
func RenameNoReplace(oldpath, newpath string) error {
	return renameNoReplace(oldpath, newpath)
}

// linkRename is the portable fallback: a hard link is created only when newpath is free, then oldpath is
// removed. If the removal fails the file is at newpath already, and the error matches ErrNotRemoved.
func linkRename(oldpath, newpath string) error {
	if err := os.Link(oldpath, newpath); err != nil {
		return err
	}
	if err := os.Remove(oldpath); err != nil {
		return fmt.Errorf("%w: %v", ErrNotRemoved, err)
	}
	return nil
}
