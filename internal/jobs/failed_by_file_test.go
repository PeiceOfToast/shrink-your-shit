// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file is counted once, by its newest job: a failure only counts while it is the file's newest job.

// failedCounts reads "failed" from every place the Jobs tab, the library card, the overview and the
// activity figures count it.
func failedCounts(t *testing.T, s *Store, libID int64) map[string]int {
	t.Helper()
	_, total, err := s.FailedJobs(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _, cards, err := s.FileList(FileFilter{Show: CardProcessed, LibraryID: libID})
	if err != nil {
		t.Fatal(err)
	}
	tot, err := s.TotalsOf()
	if err != nil {
		t.Fatal(err)
	}
	act, err := s.ActivitySummary()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]int{
		"jobs list":     total,
		"library card":  cards[CardFailed],
		"totals":        tot.Summary.Failed,
		"library count": tot.Counts[libID].Failed,
		"activity":      act.Failed,
	}
}

// want checks every count is want.
func want(t *testing.T, got map[string]int, n int) {
	t.Helper()
	for where, c := range got {
		if c != n {
			t.Errorf("%s counts %d failed, want %d (all: %v)", where, c, n, got)
		}
	}
}

// failOnce enqueues path, claims and fails its job, and returns the job id.
func failOnce(t *testing.T, s *Store, libID int64, path string) int64 {
	t.Helper()
	id, err := s.Enqueue(Job{LibraryID: libID, Path: path, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(id, "convert_failed", "boom"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFailedOldJobThenNewerDoneCountsZero(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	failOnce(t, s, l.ID, "/m/a.mkv")
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(id, "/m/a.mkv", 400); err != nil {
		t.Fatal(err)
	}
	want(t, failedCounts(t, s, l.ID), 0)
}

func TestTwoFailedJobsForOneFileCountOne(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	failOnce(t, s, l.ID, "/m/a.mkv")
	failOnce(t, s, l.ID, "/m/a.mkv")
	want(t, failedCounts(t, s, l.ID), 1)
}

func TestTwoFailedFilesCountTwo(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.SetPaused(false)
	failOnce(t, s, l.ID, "/m/a.mkv")
	failOnce(t, s, l.ID, "/m/b.mkv")
	want(t, failedCounts(t, s, l.ID), 2)
}
