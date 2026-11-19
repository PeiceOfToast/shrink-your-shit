// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
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
func gpuPercent(now time.Time) *float64 {
	gpuUse.mu.Lock()
	defer gpuUse.mu.Unlock()
	if gpuUse.ok && now.Sub(gpuUse.at) < 5*time.Second {
		return gpuUse.val
	}
	gpuUse.val, gpuUse.at, gpuUse.ok = readNvidiaSMI(), now, true
	return gpuUse.val
}

// gpuUse is gpuPercent's cache.
var gpuUse struct {
	mu  sync.Mutex
	at  time.Time
	val *float64
	ok  bool
}

// readNvidiaSMI is the reading gpuPercent caches; tests replace it.
var readNvidiaSMI = func() *float64 {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	out, err := exec.Command(path, "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits").Output()
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
