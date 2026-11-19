// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A conversion that left its original where it was is could-free, not waiting, and the daily entry has
// it next to waiting: the saved bytes split as waiting + could free, with nothing freed yet.
func TestDailyCouldFreeForAKeptOriginal(t *testing.T) {
	s := open(t)
	// files go under l.Path, the stored (resolved) folder: macOS /var is /private/var, Windows RUNNER~1 is the long name
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	day := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 12, 0, 0, 0, time.Local)

	// a.mkv: converted beside its original, which stays where it is: saves 600, all could free
	a, _ := s.Enqueue(Job{LibraryID: l.ID, Path: filepath.Join(l.Path, "a.mkv"), SizeBefore: 1000})
	s.Claim("w")
	s.Complete(a, filepath.Join(l.Path, "a.hevc.mkv"), 400)

	// b.mkv: replaced, its original kept in the trash: saves 600, all waiting
	b, _ := s.Enqueue(Job{LibraryID: l.ID, Path: filepath.Join(l.Path, "b.mkv"), SizeBefore: 1000})
	s.Claim("w")
	s.Complete(b, filepath.Join(l.Path, "b.mkv"), 400)
	ib, err := s.BeginSwap(Original{JobID: b, LibraryID: l.ID, Path: filepath.Join(l.Path, "b.mkv"), Trash: filepath.Join(l.Path, ".syp-trash", "b.mkv"), NewPath: filepath.Join(l.Path, "b.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSwap(ib, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	for id := range map[int64]bool{a: true, b: true} {
		if _, err := s.db.Exec(`UPDATE events SET at=? WHERE job_id=? AND to_status='done'`, day.UTC().Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}

	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	dk := day.Format("2006-01-02")
	if tot.SavedByDay[dk] != 1200 || tot.WaitingByDay[dk] != 600 || tot.CouldFreeByDay[dk] != 600 {
		t.Errorf("by day: saved %d, waiting %d, could free %d; want 1200, 600, 600", tot.SavedByDay[dk], tot.WaitingByDay[dk], tot.CouldFreeByDay[dk])
	}
	days, err := s.Days(7, time.Now(), tot)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if d.Day != dk {
			continue
		}
		if d.SavedBytes != 1200 || d.WaitingBytes != 600 || d.CouldFreeBytes != 600 || d.FreedBytes != 0 {
			t.Errorf("daily entry %+v: want saved 1200, waiting 600, could free 600, freed 0", d)
		}
	}
}
