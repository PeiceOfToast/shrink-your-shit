// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"
)

// #607: the window a caller passes is a full time (now-24h), while a row's hour is the bucket's start. A
// file done inside the window's first, partial hour belongs to that hour: it was dropped when the bucket's
// start sorted before the window's start.
func TestThroughputKeepsTheWindowFirstPartialHour(t *testing.T) {
	s := open(t)
	done := time.Date(2026, 11, 20, 15, 20, 0, 0, time.UTC)
	s.now = func() time.Time { return done }
	id, err := s.Enqueue(Job{Path: filepath.Join(t.TempDir(), "in.mkv"), SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(id, filepath.Join(t.TempDir(), "out.mkv"), 400); err != nil {
		t.Fatal(err)
	}

	hours, err := s.Throughput("2026-11-20T15:17:00Z") // three minutes into the 15:00 bucket
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 1 || hours[0].HourStart != "2026-11-20T15:00:00Z" || hours[0].Completed != 1 || hours[0].SavedBytes != 600 {
		t.Errorf("hours %+v; want the 15:00 bucket with the file's 600 bytes", hours)
	}
}
