// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package sysload

import (
	"runtime"
	"testing"
	"time"
)

// The first sample has memory and no processor figure; the next has both (Linux).
func TestMeter(t *testing.T) {
	var m Meter
	first := m.Sample()
	if runtime.GOOS != "linux" {
		if first.CPUPercent != nil || first.MemTotalBytes != nil {
			t.Error("not measured here: nil")
		}
		return
	}
	if first.CPUPercent != nil || first.MemTotalBytes == nil || *first.MemTotalBytes == 0 || *first.MemUsedBytes > *first.MemTotalBytes {
		t.Fatalf("first %+v", first)
	}
	time.Sleep(50 * time.Millisecond)
	next := m.Sample()
	if next.CPUPercent == nil || *next.CPUPercent < 0 || *next.CPUPercent > 100 {
		t.Errorf("next cpu %v", next.CPUPercent)
	}
}
