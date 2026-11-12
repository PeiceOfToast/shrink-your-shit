// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package sysload measures this computer's processor and memory use for the Workers page (V7d). Where
// it can't measure (not Linux yet), the figures are nil: shown as "not measured", never guessed.
package sysload

import (
	"math"
	"sync"
)

// Load is one measurement.
type Load struct {
	CPUPercent    *float64 `json:"cpuPercent"`
	MemUsedBytes  *uint64  `json:"memUsedBytes"`
	MemTotalBytes *uint64  `json:"memTotalBytes"`
}

// Meter measures the processor's use between two of its samples (the first has none yet).
type Meter struct {
	mu          sync.Mutex
	idle, total uint64
}

// Sample is the load now: the processor since the last sample, the memory now.
func (m *Meter) Sample() Load {
	var l Load
	idle, total, ok := cpuTimes()
	m.mu.Lock()
	if ok && m.total > 0 && total > m.total {
		busy := 100 * (1 - float64(idle-m.idle)/float64(total-m.total))
		l.CPUPercent = Clamp(&busy)
	}
	if ok {
		m.idle, m.total = idle, total
	}
	m.mu.Unlock()
	if used, all, ok := memory(); ok {
		l.MemUsedBytes, l.MemTotalBytes = &used, &all
	}
	return l
}

// Clamp keeps a processor figure within 0..100; NaN or an infinity is "not measured" (nil). A worker's
// report is clamped the same way on the server (review da46389).
func Clamp(p *float64) *float64 {
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
		return nil
	}
	v := math.Min(100, math.Max(0, *p))
	return &v
}
