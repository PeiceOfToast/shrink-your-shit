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
// library's jobs and for a job from before libraries.
func TestTotalsOfMatchTheSeparateFigures(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	for i, size := range []int64{1000, 2000} {
		id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: fmt.Sprint(i), SizeBefore: size})
		s.Claim("w")
		s.Complete(id, fmt.Sprint(i), size/4) // replaced in place
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
	days, err := s.Days(30, time.Now(), tot.FreedByDay)
	if err != nil {
		t.Fatal(err)
	}
	var freed int64
	for _, d := range days {
		freed += d.FreedBytes
	}
	if freed != a.FreedBytes || freed == 0 {
		t.Errorf("freed over the days %d, want the summary's %d (and some)", freed, a.FreedBytes)
	}
}
