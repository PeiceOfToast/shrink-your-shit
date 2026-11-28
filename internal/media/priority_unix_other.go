// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix && !linux

package media

// lowerIOPriority has no portable call on the other Unix systems (macOS, the BSDs): the CPU priority
// in lowerPriority is the whole of it there, so this is a no-op.
func lowerIOPriority(pid int) {}
