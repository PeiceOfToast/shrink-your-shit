// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/sysload"
)

// Where the kernel shows the GPUs. Tests point these at a fake tree.
var (
	drmDir     = "/sys/class/drm"
	i915PMUDir = "/sys/bus/event_source/devices/i915"
)

// readAMDGPU is the busiest AMD card's busy percent, from the gpu_busy_percent file amdgpu writes for
// each card. Nil when no card has the file.
func readAMDGPU(dir string) *float64 {
	files, _ := filepath.Glob(filepath.Join(dir, "card*", "device", "gpu_busy_percent"))
	var best *float64
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		best = busiest(best, &v)
	}
	return sysload.Clamp(best)
}

// intelEngine is one busy counter the i915 PMU exposes: its name and the perf config that selects it.
type intelEngine struct {
	name   string
	config uint64
}

// readIntelPMU lists the i915 PMU at dir: the perf type, the CPU its counters are opened on, and one
// entry per "<engine>-busy" event (rcs0, vcs0, ...). Counters are in nanoseconds.
func readIntelPMU(dir string) (typ uint32, cpu int, engines []intelEngine, err error) {
	raw, err := os.ReadFile(filepath.Join(dir, "type"))
	if err != nil {
		return 0, 0, nil, err
	}
	t, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		return 0, 0, nil, err
	}
	raw, err = os.ReadFile(filepath.Join(dir, "cpumask"))
	if err != nil {
		return 0, 0, nil, err
	}
	// The mask is "0" or a list like "0-3"; the first CPU is enough for an uncore counter.
	first := strings.FieldsFunc(strings.TrimSpace(string(raw)), func(r rune) bool { return r == ',' || r == '-' })
	if len(first) == 0 {
		return 0, 0, nil, errors.New("empty cpumask")
	}
	if cpu, err = strconv.Atoi(first[0]); err != nil {
		return 0, 0, nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if err != nil {
		return 0, 0, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "-busy") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "events", name))
		if err != nil {
			continue
		}
		cfg, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "config=")
		if !ok {
			continue
		}
		c, err := strconv.ParseUint(cfg, 0, 64)
		if err != nil {
			continue
		}
		engines = append(engines, intelEngine{name: strings.TrimSuffix(name, "-busy"), config: c})
	}
	return uint32(t), cpu, engines, nil
}

// pmuCounter is an open perf counter: read returns its running total.
type pmuCounter interface {
	read() (uint64, error)
	close()
}

// The Intel reading's seams: openPMUCounter is the perf call (platform-specific, tests replace it);
// the clock and wait measure the window between the two counter reads.
var (
	openPMUCounter = openPerfCounter
	intelClock     = time.Now
	intelWait      = time.Sleep
	intelWindow    = 200 * time.Millisecond
)

// readIntelGPU is the busiest Intel engine's busy percent (the same counters intel_gpu_top shows), read
// from the i915 PMU over intelWindow. Nil when there is no i915 PMU, or the kernel refuses to open its
// counters: that needs CAP_PERFMON, or a perf_event_paranoid the process may use.
func readIntelGPU(dir string) *float64 {
	typ, cpu, engines, err := readIntelPMU(dir)
	if err != nil || len(engines) == 0 {
		return nil
	}
	counters := make([]pmuCounter, 0, len(engines))
	defer func() {
		for _, c := range counters {
			c.close()
		}
	}()
	for _, e := range engines {
		c, err := openPMUCounter(typ, e.config, cpu)
		if err != nil {
			return nil
		}
		counters = append(counters, c)
	}
	start, ok := readCounters(counters)
	if !ok {
		return nil
	}
	t0 := intelClock()
	intelWait(intelWindow)
	elapsed := intelClock().Sub(t0)
	end, ok := readCounters(counters)
	if !ok {
		return nil
	}
	return busyPercent(start, end, elapsed)
}

// readCounters reads every counter once, in order.
func readCounters(cs []pmuCounter) ([]uint64, bool) {
	out := make([]uint64, len(cs))
	for i, c := range cs {
		v, err := c.read()
		if err != nil {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// busyPercent is the busiest engine's share of the window: its busy nanoseconds over the window's
// length. Nil when the window is empty, the counts do not line up, or a counter went backwards.
func busyPercent(start, end []uint64, elapsed time.Duration) *float64 {
	if elapsed <= 0 || len(start) == 0 || len(start) != len(end) {
		return nil
	}
	var best *float64
	for i := range start {
		if end[i] < start[i] {
			return nil
		}
		v := float64(end[i]-start[i]) / float64(elapsed) * 100
		best = busiest(best, &v)
	}
	return sysload.Clamp(best)
}
