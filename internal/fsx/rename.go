// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import "os"

// RenameNoReplace moves oldpath to newpath and fails with an error that matches os.ErrExist when newpath is
// already there. Nothing is ever overwritten (L-SAFE-10). Each OS has its own file: Linux renameat2 with
// RENAME_NOREPLACE, Windows MoveFileEx without the replace flag, and link then unlink elsewhere.
func RenameNoReplace(oldpath, newpath string) error {
	return renameNoReplace(oldpath, newpath)
}

// linkRename is the portable fallback: a hard link is created only when newpath is free, then oldpath is
// removed. If the removal fails the file is at newpath already, so the error is wrapped as ErrSourceLeftBehind
// by the caller (safety), not here.
func linkRename(oldpath, newpath string) error {
	if err := os.Link(oldpath, newpath); err != nil {
		return err
	}
	return os.Remove(oldpath)
}
