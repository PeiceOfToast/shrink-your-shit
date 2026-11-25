// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix

package web

// folderOwner: systems without Unix owners have nothing to hand a file to.
func folderOwner(dir string) (uid, gid int, ok bool) { return 0, 0, false }
