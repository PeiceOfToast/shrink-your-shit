// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A requeued file is one file: its latest end decides its counts, on the day of that end.

// noonDaysAgo is noon, local time, that many days before now.
func noonDaysAgo(daysAgo int) time.Time {
	n := time.Now().AddDate(0, 0, -daysAgo)
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.Local)
}

// moveEnd sets the at of job id's nth event to status to, so a test can put it on a given day.
func moveEnd(t *testing.T, s *Store, id int64, to string, nth int, at time.Time) {
	t.Helper()
	ev, err := s.JobEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range ev {
		if e.To != to {
			continue
		}
		if seen++; seen == nth {
			if _, err := s.db.Exec(`UPDATE events SET at=? WHERE seq=?`, at.UTC().Format(time.RFC3339), e.Seq); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("job %d has no %d event to %q", id, nth, to)
}

// dayCounts is the overview's per-day figures for the last 7 days, by day.
func dayCounts(t *testing.T, s *Store) map[string]Day {
	t.Helper()
	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	days, err := s.Days(7, time.Now(), tot)
	if err != nil {
		t.Fatal(err)
	}
	byDay := map[string]Day{}
	for _, d := range days {
		byDay[d.Day] = d
	}
	return byDay
}

// sumDays is the failed and completed counts over every day listed.
func sumDays(days map[string]Day) (failed, completed int) {
	for _, d := range days {
		failed += d.Failed
		completed += d.Completed
	}
	return failed, completed
}

func TestFailThenPassCountsAsDone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Fail(id, "convert_failed", "boom")
	s.Retry(id)
	s.Claim("w")
	s.Complete(id, "/m/a.mkv", 400)
	moveEnd(t, s, id, "failed", 1, noonDaysAgo(3))
	moveEnd(t, s, id, "done", 1, noonDaysAgo(1))

	days := dayCounts(t, s)
	if failed, done := sumDays(days); failed != 0 || done != 1 {
		t.Errorf("fail then pass: failed %d, done %d over the days; want 0 and 1 (%+v)", failed, done, days)
	}
	if got := days[noonDaysAgo(1).Format("2006-01-02")]; got.Completed != 1 {
		t.Errorf("done on the day it passed: %+v", got)
	}
	if tot, _ := s.TotalsOf(); tot.Summary.Failed != 0 {
		t.Errorf("failed total %d, want 0", tot.Summary.Failed)
	}
	if _, total, _ := s.FailedJobs(10, 0); total != 0 {
		t.Errorf("the Failed list still has %d file(s) that passed", total)
	}
	if ends, _ := s.JobEndsSince(time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)); ends["failed"] != 0 || ends["kept"] != 0 {
		t.Errorf("last 24 hours counts a failure for a file that passed: %v", ends)
	}
}

func TestFailFailPassCountsAsDone(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Fail(id, "convert_failed", "boom")
	s.Retry(id)
	s.Claim("w")
	s.Fail(id, "convert_failed", "boom again")
	s.Retry(id)
	s.Claim("w")
	s.Complete(id, "/m/a.mkv", 400)
	moveEnd(t, s, id, "failed", 1, noonDaysAgo(4))
	moveEnd(t, s, id, "failed", 2, noonDaysAgo(3))
	moveEnd(t, s, id, "done", 1, noonDaysAgo(1))

	days := dayCounts(t, s)
	if failed, done := sumDays(days); failed != 0 || done != 1 {
		t.Errorf("fail, fail, pass: failed %d, done %d; want 0 and 1", failed, done)
	}
}

func TestFailThenFailCountsOnceOnTheLatestDay(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Fail(id, "convert_failed", "boom")
	s.Retry(id)
	s.Claim("w")
	s.Fail(id, "convert_failed", "boom again")
	moveEnd(t, s, id, "failed", 1, noonDaysAgo(3))
	moveEnd(t, s, id, "failed", 2, noonDaysAgo(1))

	days := dayCounts(t, s)
	if failed, _ := sumDays(days); failed != 1 {
		t.Errorf("fail then fail: %d failed over the days, want 1 (%+v)", failed, days)
	}
	if got := days[noonDaysAgo(1).Format("2006-01-02")]; got.Failed != 1 {
		t.Errorf("the failure counts on the second day: %+v", got)
	}
	if got := days[noonDaysAgo(3).Format("2006-01-02")]; got.Failed != 0 {
		t.Errorf("the first day's failure must not count: %+v", got)
	}
}
