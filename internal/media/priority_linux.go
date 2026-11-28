// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package media

import "golang.org/x/sys/unix"

// ioprioWhoProcess is the kernel's IOPRIO_WHO_PROCESS: the priority of one process.
const ioprioWhoProcess = 1

// ioprioLowest is the lowest "best effort" I/O priority: class 2 (best effort), level 7. It is the
// value `ionice -c2 -n7` sets.
const ioprioLowest = (2 << 13) | 7

// lowerIOPriority asks the kernel for the lowest best-effort I/O priority, so a conversion does not
// crowd out the person's other reads and writes. Linux offers no x/sys wrapper, so this is the raw
// syscall the ionice tool uses.
func lowerIOPriority(pid int) {
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(pid), ioprioLowest)
}
