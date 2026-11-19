// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A slow reading must not hold the cache's lock: a caller with a fresh cache gets it at once.
func TestGPUSlowReadDoesNotHoldTheCache(t *testing.T) {
	resetGPU(t) // an earlier test's overview call may have cached a reading moments ago
	ten := 10.0
	t0 := time.Now()
	readNvidiaSMI = func() *float64 { return &ten }
	if v := gpuPercent(t0); v == nil || *v != 10 {
		t.Fatalf("first reading %v, want 10", v)
	}

	release := make(chan struct{})
	entered := make(chan struct{})
	readNvidiaSMI = func() *float64 { close(entered); <-release; return nil }
	stale := make(chan struct{})
	go func() { gpuPercent(t0.Add(6 * time.Second)); close(stale) }() // the cache is stale: this one reads and blocks
	<-entered

	got := make(chan *float64, 1)
	go func() { got <- gpuPercent(t0.Add(time.Second)) }() // still fresh: answered from the cache
	select {
	case v := <-got:
		if v == nil || *v != 10 {
			t.Errorf("cached reading %v, want 10", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a caller with a fresh cache waited for the slow read")
	}
	close(release)
	<-stale
}

// A nvidia-smi that never returns is stopped at the timeout and reads as not measured.
func TestNvidiaSMITimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script as nvidia-smi")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on this machine")
	}
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nexec %s 30\n", sleep)
	if err := os.WriteFile(filepath.Join(dir, "nvidia-smi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	start := time.Now()
	if v := queryNvidiaSMI(200 * time.Millisecond); v != nil {
		t.Errorf("a timed-out reading is %v, want nil", *v)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the read took %v; it should stop at its timeout", took)
	}
}
