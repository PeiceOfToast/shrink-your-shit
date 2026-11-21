// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package sysload

import "testing"

// The host's meminfo in a container: 24.9 GB shown, the LXC's cgroup limit 4 GB. The total is the limit and the
// used figure is the cgroup's usage, not the host's.
func TestMemoryFromUsesTheCgroupLimitBelowTheHost(t *testing.T) {
	meminfo := "MemTotal:       26000000 kB\nMemAvailable:   20000000 kB\n"
	used, total, ok := memoryFrom(meminfo, 4<<30, 1<<30)
	if !ok || total != 4<<30 || used != 1<<30 {
		t.Fatalf("used %d total %d ok %v; want 1 GiB of 4 GiB", used, total, ok)
	}
}

// No limit, or v1's unlimited number (above the host's total), keeps the host's figures.
func TestMemoryFromKeepsTheHostWithoutALimit(t *testing.T) {
	meminfo := "MemTotal:       26000000 kB\nMemAvailable:   20000000 kB\n"
	for _, limit := range []uint64{0, 9223372036854771712} {
		used, total, ok := memoryFrom(meminfo, limit, 1<<30)
		if !ok || total != 26000000*1024 || used != 6000000*1024 {
			t.Errorf("limit %d: used %d total %d ok %v", limit, used, total, ok)
		}
	}
}

// A limit whose usage cannot be read, or reads above the limit, is not measured rather than a made-up figure.
func TestMemoryFromRefusesAnUnreadableCgroupUsage(t *testing.T) {
	meminfo := "MemTotal:       26000000 kB\nMemAvailable:   20000000 kB\n"
	if _, _, ok := memoryFrom(meminfo, 4<<30, 0); ok {
		t.Error("unknown usage under a limit should not be measured")
	}
	if _, _, ok := memoryFrom(meminfo, 4<<30, 5<<30); ok {
		t.Error("usage above the limit should not be measured")
	}
}
