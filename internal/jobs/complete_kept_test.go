// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"
)

// CompleteKeptJobs reads each running job's own kept original, not any job's: with two finished swaps, each
// job records its own new file. Without the job_id join the newest row's file lands on both (review-583, M4).
func TestCompleteKeptJobsReadsEachJobsOwnOriginal(t *testing.T) {
	s := open(t)
	a, err := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Enqueue(Job{Library: "/m", Path: "/m/b.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	// both running: the process stopped between the swap and recording the job
	for _, id := range []int64{a, b} {
		if j, err := s.Claim("w"); err != nil || j.ID != id {
			t.Fatalf("claim %d: %+v %v", id, j, err)
		}
	}
	due := time.Now().Add(7 * 24 * time.Hour)
	for _, c := range []struct {
		id       int64
		path     string
		trash    string
		newBytes int64
	}{
		{a, "/m/a.mkv", "/m/.syp-trash/a.1.mkv", 10},
		{b, "/m/b.mkv", "/m/.syp-trash/b.1.mkv", 20},
	} {
		row, err := s.BeginSwap(Original{JobID: c.id, Path: c.path, Trash: c.trash, NewBytes: c.newBytes})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishSwap(row, due); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CompleteKeptJobs()
	if err != nil || n != 2 {
		t.Fatalf("complete kept jobs: %d %v, want 2", n, err)
	}
	for _, c := range []struct {
		id       int64
		path     string
		newBytes int64
	}{
		{a, "/m/a.mkv", 10},
		{b, "/m/b.mkv", 20},
	} {
		j, err := s.Get(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != Done || j.OutputPath != c.path || j.SizeAfter != c.newBytes {
			t.Errorf("job %d: status %s, output %q, size %d; want done, %q, %d",
				c.id, j.Status, j.OutputPath, j.SizeAfter, c.path, c.newBytes)
		}
	}
}
