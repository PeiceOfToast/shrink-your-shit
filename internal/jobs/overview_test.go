// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// TotalsOf is one pass; it must give what ActivitySummary, Counts and Days give on their own, for a
// library's jobs and for a job from before libraries, and each freed saving must land on its own day.
func TestTotalsOfMatchTheSeparateFigures(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)

	// first, so the queue claims it: its original was left beside the new file and found gone three days
	// ago, so it is freed on that day, not on the day it was converted (updated)
	g, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "gone", SizeBefore: 4000})
	s.Claim("w")
	s.Complete(g, "gone.hevc.mkv", 1000) // saves 3000
	goneAt := time.Now().Add(-72 * time.Hour)
	if _, err := s.db.Exec(`UPDATE jobs SET original_gone_at=?, original_changed=0 WHERE id=?`, goneAt.UTC().Format(time.RFC3339), g); err != nil {
		t.Fatal(err)
	}

	for i, size := range []int64{1000, 2000} {
		id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: fmt.Sprint(i), SizeBefore: size})
		s.Claim("w")
		s.Complete(id, fmt.Sprint(i), size/4) // replaced in place: freed on the day it was converted
	}
	f, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "f"})
	s.Claim("w")
	s.Fail(f, "convert_failed", "x")
	s.Enqueue(Job{LibraryID: l.ID, Path: "q1"})
	s.Enqueue(Job{Library: "old", Path: "old1", SizeBefore: 500}) // from before libraries: no library_id
	s.Claim("w")

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.ActivitySummary()
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if tot.Summary != a {
		t.Errorf("summary %+v, want %+v", tot.Summary, a)
	}
	if !reflect.DeepEqual(tot.Counts, c) {
		t.Errorf("counts %+v, want %+v", tot.Counts, c)
	}

	// per day: the converted files (750 + 1500) on today, the gone original's 3000 three days back
	today := time.Now().In(time.Local).Format("2006-01-02")
	goneDay := goneAt.In(time.Local).Format("2006-01-02")
	if goneDay == today {
		t.Fatalf("the gone day is today (%s): the test cannot tell the days apart", today)
	}
	wantByDay := map[string]int64{today: 750 + 1500, goneDay: 3000}
	if !reflect.DeepEqual(tot.FreedByDay, wantByDay) {
		t.Errorf("freed by day %v, want %v", tot.FreedByDay, wantByDay)
	}
	days, err := s.Days(30, time.Now(), tot.FreedByDay)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, d := range days {
		if d.FreedBytes != 0 {
			got[d.Day] = d.FreedBytes
		}
	}
	if !reflect.DeepEqual(got, wantByDay) {
		t.Errorf("the days' freed bytes %v, want %v", got, wantByDay)
	}
}
