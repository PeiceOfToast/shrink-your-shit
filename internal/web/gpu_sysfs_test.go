// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTree writes each file under dir, creating folders as needed.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// An AMD card's gpu_busy_percent is read, and the busiest card wins; a card without the file is skipped.
func TestAMDBusyFromSysfs(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"card0/device/gpu_busy_percent": "12\n",
		"card1/device/gpu_busy_percent": "47\n",
		"card2/device/vendor":           "0x1002\n",
	})
	if v := readAMDGPU(dir); v == nil || *v != 47 {
		t.Fatalf("AMD reading %v, want 47", v)
	}
	if v := readAMDGPU(t.TempDir()); v != nil {
		t.Fatalf("AMD reading %v with no card files, want nil", *v)
	}
}

// The i915 PMU's busy events are the engines; the perf type, CPU and each config come from its files.
func TestIntelEnginesFromPMU(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"type":                    "32\n",
		"cpumask":                 "0\n",
		"events/rcs0-busy":        "config=0x0\n",
		"events/rcs0-busy.unit":   "ns\n",
		"events/vcs0-busy":        "config=0x2000\n",
		"events/actual-frequency": "config=0x1\n",
		"events/rcs0-sema":        "config=0x10000\n",
	})
	typ, cpu, engines, err := readIntelPMU(dir)
	if err != nil {
		t.Fatal(err)
	}
	if typ != 32 || cpu != 0 {
		t.Errorf("type %d cpu %d, want 32 and 0", typ, cpu)
	}
	if len(engines) != 2 || engines[0] != (intelEngine{"rcs0", 0}) || engines[1] != (intelEngine{"vcs0", 0x2000}) {
		t.Errorf("engines %+v, want rcs0 config 0 and vcs0 config 0x2000", engines)
	}
	if _, _, _, err := readIntelPMU(t.TempDir()); err == nil {
		t.Error("a folder with no PMU reads without error")
	}
}

// fakeCounter reads start on its first read and end on its second.
type fakeCounter struct {
	start, end uint64
	reads      int
}

func (c *fakeCounter) read() (uint64, error) {
	c.reads++
	if c.reads == 1 {
		return c.start, nil
	}
	return c.end, nil
}

func (c *fakeCounter) close() {}

// stubIntel replaces the perf call and the clock for one test: open maps each config to its counter, and
// the window is exactly one second.
func stubIntel(t *testing.T, open func(uint32, uint64, int) (pmuCounter, error)) {
	t.Helper()
	realOpen, realClock, realWait := openPMUCounter, intelClock, intelWait
	now := time.Unix(1000, 0)
	openPMUCounter = open
	intelClock = func() time.Time { return now }
	intelWait = func(time.Duration) { now = now.Add(time.Second) }
	t.Cleanup(func() { openPMUCounter, intelClock, intelWait = realOpen, realClock, realWait })
}

// The busiest engine's busy time over the window is the reading: rcs0 at 25% and vcs0 at 80% read 80.
func TestIntelBusyFromCounters(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"type":             "32\n",
		"cpumask":          "0\n",
		"events/rcs0-busy": "config=0x0\n",
		"events/vcs0-busy": "config=0x2000\n",
	})
	counters := map[uint64]*fakeCounter{
		0:      {start: 0, end: 250_000_000},
		0x2000: {start: 100, end: 800_000_100},
	}
	stubIntel(t, func(typ uint32, cfg uint64, cpu int) (pmuCounter, error) {
		if typ != 32 || cpu != 0 {
			t.Errorf("opened type %d on cpu %d, want 32 on cpu 0", typ, cpu)
		}
		return counters[cfg], nil
	})
	v := readIntelGPU(dir)
	if v == nil || *v < 79.99 || *v > 80.01 {
		t.Fatalf("Intel reading %v, want 80", v)
	}
}

// Without the counters (no CAP_PERFMON), the reading is not measured rather than a guess.
func TestIntelWithoutCounterAccessIsNil(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"type":             "32\n",
		"cpumask":          "0\n",
		"events/rcs0-busy": "config=0x0\n",
	})
	stubIntel(t, func(uint32, uint64, int) (pmuCounter, error) { return nil, errors.New("EACCES") })
	if v := readIntelGPU(dir); v != nil {
		t.Fatalf("Intel reading %v without counter access, want nil", *v)
	}
}

// The percent is a window's share, never negative, never over 100, and nil when the window cannot be
// trusted.
func TestBusyPercentRules(t *testing.T) {
	if v := busyPercent([]uint64{0}, []uint64{500_000_000}, 0); v != nil {
		t.Errorf("empty window gave %v, want nil", *v)
	}
	if v := busyPercent([]uint64{10}, []uint64{5}, time.Second); v != nil {
		t.Errorf("a counter that went backwards gave %v, want nil", *v)
	}
	if v := busyPercent([]uint64{0}, []uint64{3_000_000_000}, time.Second); v == nil || *v != 100 {
		t.Errorf("busy beyond the window gave %v, want 100", v)
	}
}
