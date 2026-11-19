// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file converted twice counts its saving once: by its newest conversion, on that conversion's day.
// The older conversion's 600 saved must not appear, or a republished workflow would count it again.
func TestSavedCountsOnlyTheNewestConversion(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	noon := func(daysAgo int) time.Time {
		n := time.Now().AddDate(0, 0, -daysAgo)
		return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.Local)
	}
	older, newer := noon(9), noon(3)

	first, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/c.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(first, "/m/c.mkv", 400) // saves 600

	second, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/c.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(second, "/m/c.mkv", 300) // saves 700, the newest

	for id, at := range map[int64]time.Time{first: older, second: newer} {
		if _, err := s.db.Exec(`UPDATE events SET at=? WHERE job_id=? AND to_status='done'`, at.UTC().Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, b := range tot.SavedByDay {
		sum += b
	}
	if sum != 700 {
		t.Errorf("saved by day %v: total %d, want 700 (the newest conversion only)", tot.SavedByDay, sum)
	}
	if got := tot.SavedByDay[newer.Format("2006-01-02")]; got != 700 {
		t.Errorf("saved on the newer conversion's day = %d, want 700", got)
	}
	if got := tot.SavedByDay[older.Format("2006-01-02")]; got != 0 {
		t.Errorf("saved on the older conversion's day = %d, want 0", got)
	}
}
