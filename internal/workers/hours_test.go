// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workers

import (
	"testing"
	"time"
)

// The hours a worker may take new jobs (page 10): from counts, to does not, and a window whose end is
// earlier than its start runs overnight.

func TestInsideADaysHours(t *testing.T) {
	h := Hours{From: "09:00", To: "17:00"}
	for _, c := range []struct {
		hh, mm int
		want   bool
	}{
		{8, 59, false},
		{9, 0, true},
		{12, 30, true},
		{16, 59, true},
		{17, 0, false},
		{23, 0, false},
	} {
		if got := h.Inside(time.Date(2026, 11, 28, c.hh, c.mm, 0, 0, time.Local)); got != c.want {
			t.Errorf("09:00-17:00 inside %02d:%02d = %v, want %v", c.hh, c.mm, got, c.want)
		}
	}
}

func TestInsideHoursThatRunOvernight(t *testing.T) {
	h := Hours{From: "22:00", To: "06:00"}
	for _, c := range []struct {
		hh, mm int
		want   bool
	}{
		{21, 59, false},
		{22, 0, true},
		{23, 30, true},
		{0, 0, true},
		{5, 59, true},
		{6, 0, false},
		{12, 0, false},
	} {
		if got := h.Inside(time.Date(2026, 11, 28, c.hh, c.mm, 0, 0, time.Local)); got != c.want {
			t.Errorf("22:00-06:00 inside %02d:%02d = %v, want %v", c.hh, c.mm, got, c.want)
		}
	}
}

// The worker's own settings decide, and no hours means any time.
func TestAWorkerWithoutHoursAllowsAnyTime(t *testing.T) {
	w := Worker{Slots: 1}
	if !w.AllowsWork(time.Date(2026, 11, 28, 3, 17, 0, 0, time.Local)) {
		t.Error("no hours refused a job at 03:17")
	}
	w.Hours = &Hours{From: "09:00", To: "17:00"}
	if w.AllowsWork(time.Date(2026, 11, 28, 3, 17, 0, 0, time.Local)) {
		t.Error("09:00-17:00 allowed a job at 03:17")
	}
}
