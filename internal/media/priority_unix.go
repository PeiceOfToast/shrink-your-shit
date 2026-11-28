// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package media

import "golang.org/x/sys/unix"

// lowerPriority puts a started tool below the rest of the machine's work: a lower CPU priority (nice
// 10), and on Linux a lower I/O priority too. Both are best effort: a system that refuses either still
// converts the file, and a tool that already exited is not a problem.
func lowerPriority(pid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 10)
	lowerIOPriority(pid)
}
