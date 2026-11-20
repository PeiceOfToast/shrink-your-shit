// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package web

import (
	"syscall"
	"unsafe"
)

// perfAttr is the first 64 bytes of the kernel's perf_event_attr (PERF_ATTR_SIZE_VER0): all a plain
// counting event needs. The kernel reads the size it is given and zeroes the rest.
type perfAttr struct {
	Type, Size                                          uint32
	Config, SamplePeriod, SampleType, ReadFormat, Flags uint64
	WakeupEvents, BpType                                uint32
	Config1                                             uint64
}

// perfFlagFDCloexec is PERF_FLAG_FD_CLOEXEC: the counter does not leak into child processes.
const perfFlagFDCloexec = 1 << 3

// perfCounterFD is an open perf counter file descriptor.
type perfCounterFD struct{ fd int }

// openPerfCounter opens a system-wide counter of a PMU (pid -1, the given CPU), the way intel_gpu_top
// does. It fails with EACCES or EPERM without CAP_PERFMON and a permissive perf_event_paranoid.
func openPerfCounter(typ uint32, cfg uint64, cpu int) (pmuCounter, error) {
	attr := perfAttr{Type: typ, Size: uint32(unsafe.Sizeof(perfAttr{})), Config: cfg}
	fd, _, errno := syscall.Syscall6(syscall.SYS_PERF_EVENT_OPEN,
		uintptr(unsafe.Pointer(&attr)), ^uintptr(0), uintptr(cpu), ^uintptr(0), perfFlagFDCloexec, 0)
	if errno != 0 {
		return nil, errno
	}
	return &perfCounterFD{fd: int(fd)}, nil
}

// read returns the counter's running total: a PMU counter reads as one 64-bit value.
func (c *perfCounterFD) read() (uint64, error) {
	var buf [8]byte
	n, err := syscall.Read(c.fd, buf[:])
	if err != nil {
		return 0, err
	}
	if n != len(buf) {
		return 0, syscall.EIO
	}
	return *(*uint64)(unsafe.Pointer(&buf[0])), nil
}

func (c *perfCounterFD) close() { syscall.Close(c.fd) }
