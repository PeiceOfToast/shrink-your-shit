// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux && !windows

package fsx

func renameNoReplace(oldpath, newpath string) error {
	return linkRename(oldpath, newpath)
}
