// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The daily saved and waiting bytes are dated by each conversion's 'done' event. A replaced original
// still in the trash is saved and waiting (no freed yet); one deleted from the trash is saved and freed
// on its delete day, not waiting.
func TestSavedAndWaitingByDay(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	noon := func(daysAgo int) time.Time {
		n := time.Now().AddDate(0, 0, -daysAgo)
		return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.Local)
	}
	dayKept, dayDeleted := noon(5), noon(2)

	// a.mkv: replaced, its original kept in the trash: saves 600, all of it waiting, nothing freed
	a, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(a, "/m/a.mkv", 400)
	ia, err := s.BeginSwap(Original{JobID: a, LibraryID: l.ID, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.mkv", NewPath: "/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSwap(ia, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// b.mkv: replaced, its original deleted from the trash: saves 800, freed on the delete day
	b, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/b.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(b, "/m/b.mkv", 200)
	ib, err := s.BeginSwap(Original{JobID: b, LibraryID: l.ID, Path: "/m/b.mkv", Trash: "/m/.syp-trash/b.mkv", NewPath: "/m/b.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSwap(ib, dayDeleted); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSwapState(ib, OriginalPurged); err != nil { // stamps delete_on as now: put it on its day
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE originals SET delete_on=? WHERE id=?`, dayDeleted.UTC().Format(time.RFC3339), ib); err != nil {
		t.Fatal(err)
	}

	// the conversions finished on their days
	for id, at := range map[int64]time.Time{a: dayKept, b: dayDeleted} {
		if _, err := s.db.Exec(`UPDATE events SET at=? WHERE job_id=? AND to_status='done'`, at.UTC().Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	kd, dd := dayKept.Format("2006-01-02"), dayDeleted.Format("2006-01-02")
	if tot.SavedByDay[kd] != 600 || tot.SavedByDay[dd] != 800 {
		t.Errorf("saved by day %v, want 600 on %s and 800 on %s", tot.SavedByDay, kd, dd)
	}
	if tot.WaitingByDay[kd] != 600 || tot.WaitingByDay[dd] != 0 {
		t.Errorf("waiting by day %v, want 600 on %s and 0 on %s", tot.WaitingByDay, kd, dd)
	}
	days, err := s.Days(30, time.Now(), tot)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		switch d.Day {
		case kd:
			if d.SavedBytes != 600 || d.WaitingBytes != 600 || d.FreedBytes != 0 {
				t.Errorf("kept day %+v: want saved 600, waiting 600, freed 0", d)
			}
		case dd:
			if d.SavedBytes != 800 || d.WaitingBytes != 0 || d.FreedBytes != 800 {
				t.Errorf("deleted day %+v: want saved 800, waiting 0, freed 800", d)
			}
		}
	}
}
