// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix

package media

// lowerPriority has nothing to do after the start here: Windows sets the process's priority class
// when it starts (tree_windows.go), and the other systems get their normal priority.
func lowerPriority(pid int) {}
