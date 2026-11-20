// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R8: a job kept because its new file was bigger changed nothing on disk, so it adds nothing to the space
// stats: not saved, not freed, not grew, and nothing waiting. Only a job that finished as done counts.
func TestKeptBecauseBiggerAddsNoSpace(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	if err := s.Keep(id, "bigger", "The new file is bigger than the original, so the original was kept as it was."); err != nil {
		t.Fatal(err)
	}

	sum, err := s.ActivitySummary()
	if err != nil {
		t.Fatal(err)
	}
	if sum.FreedBytes != 0 || sum.WaitingBytes != 0 || sum.CouldFreeBytes != 0 || sum.GrewBytes != 0 {
		t.Errorf("summary %+v: a kept job must add nothing", sum)
	}
	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	if len(tot.SavedByDay) != 0 || len(tot.WaitingByDay) != 0 || len(tot.CouldFreeByDay) != 0 {
		t.Errorf("totals %+v: a kept job must add nothing", tot)
	}
}
