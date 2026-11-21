// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
	"time"
)

// Every "saved" number tells the same story: one file's newest conversion, its original not restored,
// its new size known, saving never below 0. One database holds the shapes that used to disagree: a
// replaced file, a grown one, an unknown new size, a file converted twice, a refused (kept) job, a
// restored original and a file left beside its original.
func TestEverySavedTotalUsesOneRule(t *testing.T) {
	s := open(t)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	m := t.TempDir()
	now := time.Now()
	since := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	// done runs one job to done. out is the new file's name, "" for the same name (replaced).
	done := func(name, out string, before, after int64) int64 {
		t.Helper()
		p := filepath.Join(m, name)
		id, err := s.Enqueue(Job{Path: p, SizeBefore: before})
		check(err)
		_, err = s.Claim("w")
		check(err)
		o := p
		if out != "" {
			o = filepath.Join(m, out)
		}
		check(s.Complete(id, o, after))
		return id
	}

	// 600 saved, replaced: the original is gone.
	done("replaced.mkv", "", 1000, 400)
	// Growth is not saving: 0.
	done("grown.mkv", "", 1000, 1500)
	// New size unknown: nothing is saved by 0 bytes.
	done("unknown.mkv", "", 1000, 0)
	// Converted twice: the file counts once, by its newest conversion: 600.
	done("twice.mkv", "", 1000, 900)
	done("twice.mkv", "", 1000, 400)
	// Left beside its original: 600 saved (the original can still be removed by hand).
	done("left.mkv", "left.new.mkv", 1000, 400)
	// Refused (kept): not a conversion.
	k, err := s.Enqueue(Job{Path: filepath.Join(m, "kept.mkv"), SizeBefore: 1000})
	check(err)
	_, err = s.Claim("w")
	check(err)
	check(s.Keep(k, "check_differs", "the picture does not match"))
	// Restored: the original is this file again, so its saving counts nowhere.
	r := done("restored.mkv", "", 1000, 400)
	ri, err := s.BeginSwap(Original{JobID: r, Path: filepath.Join(m, "restored.mkv"),
		Trash: filepath.Join(m, ".syp-trash", "restored.mkv"), NewPath: filepath.Join(m, "restored.mkv")})
	check(err)
	check(s.FinishSwap(ri, now.Add(7*24*time.Hour)))
	check(s.SetSwapState(ri, OriginalRestored))

	// replaced 600 + twice 600 + left 600; grown, unknown, kept and restored add nothing.
	const want = int64(1800)
	// Converted files now: replaced, grown, unknown, twice, left (the kept job never converted and the
	// restored file is its original again).
	const wantConverted = int64(5)

	tot, err := s.TotalsOf()
	check(err)
	var byDay int64
	for _, v := range tot.SavedByDay {
		byDay += v
	}
	if byDay != want {
		t.Errorf("the overview's saved-by-day total is %d, want %d", byDay, want)
	}

	hours, err := s.Throughput(since)
	check(err)
	var hourSaved, hourDone int64
	for _, h := range hours {
		hourSaved += h.SavedBytes
		hourDone += int64(h.Completed)
	}
	if hourSaved != want {
		t.Errorf("the hourly throughput saved total is %d, want %d", hourSaved, want)
	}

	batch, err := s.BatchSince(since)
	check(err)
	if batch.Saved != want {
		t.Errorf("the batch saved total is %d, want %d", batch.Saved, want)
	}
	if batch.Converted != wantConverted {
		t.Errorf("the batch converted count is %d, want %d (a file converted twice counts once)", batch.Converted, wantConverted)
	}
	if hourDone != wantConverted {
		t.Errorf("the throughput done count is %d, want %d (a file converted twice counts once)", hourDone, wantConverted)
	}
}
