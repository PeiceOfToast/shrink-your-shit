// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"
)

// Manager 11-26 (b): originals the purge held back past their due date (U-B) made nextDeleteAt a past date.
// The soonest a kept original goes is now; a due date still ahead is unchanged.
func TestTrashDatesAreNeverPast(t *testing.T) {
	s := open(t)
	keep := func(path string, keptAt time.Time) {
		t.Helper()
		id, _ := s.Enqueue(Job{Library: "/m", Path: path})
		row, err := s.BeginSwap(Original{JobID: id, Path: path, Trash: path + ".kept", Bytes: 10})
		if err != nil {
			t.Fatal(err)
		}
		s.FinishSwap(row, keptAt.Add(7*24*time.Hour))
		if _, err := s.db.Exec(`UPDATE originals SET kept_at=? WHERE id=?`, keptAt.UTC().Format(time.RFC3339), row); err != nil {
			t.Fatal(err)
		}
	}
	at := func(s *string) time.Duration {
		t.Helper()
		if s == nil {
			t.Fatal("no date")
		}
		v, err := time.Parse(time.RFC3339, *s)
		if err != nil {
			t.Fatal(err)
		}
		return time.Until(v)
	}
	near := func(d, want time.Duration) bool { return d > want-2*time.Minute && d < want+2*time.Minute }

	keep("/m/old.mkv", time.Now().Add(-10*24*time.Hour)) // due 3 days ago, held back
	tr, err := s.TrashNow(7)
	if err != nil {
		t.Fatal(err)
	}
	if n, l := at(tr.NextDeleteAt), at(tr.LastDeleteAt); !near(n, 0) || !near(l, 0) {
		t.Errorf("all overdue: next %v, last %v from now; want both now", n, l)
	}

	keep("/m/new.mkv", time.Now().Add(-24*time.Hour)) // due in 6 days
	tr, _ = s.TrashNow(7)
	if n, l := at(tr.NextDeleteAt), at(tr.LastDeleteAt); !near(n, 0) || !near(l, 6*24*time.Hour) {
		t.Errorf("one overdue, one ahead: next %v, last %v from now; want now and 6 days", n, l)
	}
}
