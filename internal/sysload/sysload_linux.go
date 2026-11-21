// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package sysload

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// cpuTimes are the processor's idle and total time so far (/proc/stat's "cpu" line).
func cpuTimes() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range fields[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return idle, total, true
}

// memory is the memory in use and in all. In a container the total is the cgroup's limit when one is set below
// the host's memory (/proc/meminfo shows the host), and the used figure is then the cgroup's own usage.
func memory() (used, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	limit, cur := cgroupMemory()
	return memoryFrom(string(b), limit, cur)
}

// memoryFrom is memory's arithmetic: meminfo's text, the cgroup's limit (0 when none) and its usage (0 when
// unknown). A limit at or above the host's total is no limit.
func memoryFrom(meminfo string, limit, cgroupUsed uint64) (used, total uint64, ok bool) {
	var avail uint64
	for _, line := range strings.Split(meminfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	if total == 0 || avail > total {
		return 0, 0, false
	}
	if limit > 0 && limit < total {
		if cgroupUsed == 0 || cgroupUsed > limit {
			return 0, 0, false
		}
		return cgroupUsed, limit, true
	}
	return total - avail, total, true
}

// cgroupMemory is this process's memory limit and usage from its cgroup, v2 first, then v1; zeros when there is
// none or it cannot be read. "max" (no limit) and v1's huge unlimited number both come out as no limit.
func cgroupMemory() (limit, used uint64) {
	read := func(path string) (uint64, bool) {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		return n, err == nil
	}
	if l, ok := read("/sys/fs/cgroup/memory.max"); ok {
		u, _ := read("/sys/fs/cgroup/memory.current")
		return l, u
	}
	if l, ok := read("/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok {
		u, _ := read("/sys/fs/cgroup/memory/memory.usage_in_bytes")
		return l, u
	}
	return 0, 0
}
