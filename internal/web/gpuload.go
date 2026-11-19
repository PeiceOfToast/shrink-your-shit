// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/sysload"
)

// gpuPercent is this computer's GPU use for the overview's workers: NVIDIA only, read from nvidia-smi
// when it is on PATH (the busiest GPU's utilization). Anything else, or no nvidia-smi, is nil (not
// measured). The command runs at most once every five seconds: the overview is fetched on each event.
// The lock covers only the cache: the command runs without it, so a slow read never holds up others.
func gpuPercent(now time.Time) *float64 {
	gpuUse.mu.Lock()
	if gpuUse.ok && now.Sub(gpuUse.at) < 5*time.Second {
		v := gpuUse.val
		gpuUse.mu.Unlock()
		return v
	}
	gpuUse.mu.Unlock()
	v := readNvidiaSMI()
	gpuUse.mu.Lock()
	gpuUse.val, gpuUse.at, gpuUse.ok = v, now, true
	gpuUse.mu.Unlock()
	return v
}

// gpuUse is gpuPercent's cache.
var gpuUse struct {
	mu  sync.Mutex
	at  time.Time
	val *float64
	ok  bool
}

// readNvidiaSMI is the reading gpuPercent caches; tests replace it.
var readNvidiaSMI = func() *float64 { return queryNvidiaSMI(2 * time.Second) }

// queryNvidiaSMI runs nvidia-smi and reads the busiest GPU. A command that takes longer than timeout is
// stopped and reads as nil (not measured), so a wedged driver cannot hang the overview.
func queryNvidiaSMI(timeout time.Duration) *float64 {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits")
	cmd.WaitDelay = time.Second // a child that keeps the output open must not hold the wait
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var busiest *float64
	for _, line := range strings.Split(string(out), "\n") {
		v, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
		if err != nil {
			continue
		}
		if busiest == nil || v > *busiest {
			busiest = &v
		}
	}
	return sysload.Clamp(busiest)
}
