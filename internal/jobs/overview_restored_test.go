// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"reflect"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// countsOf reads every converted count the overview, the library list, the daily bars and the workflow
// page show, keyed by name. Skipped is zeroed in each: a restored file's record is a skipped file by design
// (reason 'restored'), which is a record, not a conversion, and is checked on its own by the test.
func countsOf(t *testing.T, s *Store, l Library, flowID int64, since string, now time.Time) map[string]any {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	tot, err := s.TotalsOf()
	check(err)
	tot.Summary.Skipped = 0
	lib, err := s.LibraryTotalsOf(l)
	check(err)
	lib.Skipped = 0
	days, err := s.Days(7, now, tot)
	check(err)
	done, err := s.DoneCount()
	check(err)
	sp, sok, spg, tok, err := s.Averages()
	check(err)
	runs, err := s.WorkflowRunsSince(flowID, since)
	check(err)
	runs.Skipped = 0
	hours, err := s.Throughput(since)
	check(err)
	ends, err := s.JobEndsSince(since)
	check(err)
	return map[string]any{
		"overview totals": tot,
		"library totals":  lib,
		"daily bars":      days,
		"done count":      done,
		"averages":        []any{sp, sok, spg, tok},
		"workflow runs":   runs,
		"throughput":      hours,
		"latest ends":     ends,
	}
}

// A restored original is the original file again, so its conversion counts nowhere as converted: every
// converted count reads what it read before the conversion, after the restore. The baseline file c stays
// converted throughout, so the counts are not all zero.
func TestRestoredOriginalCountsNowhere(t *testing.T) {
	s := open(t)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.AddWorkflow("Fresh", workflow.Preset())
	check(err)
	d, _, err := s.Draft(w.ID)
	check(err)
	saved, err := s.SaveDraft(w.ID, d.Doc, workflow.Layout{}, d.Etag)
	check(err)
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	_, err = s.Publish(w.ID, saved.Etag, tm)
	check(err)
	l, err := s.AddLibraryOnFlow("Movies", "/m", w.ID, true)
	check(err)
	now := time.Now()
	since := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	// c.mkv: converted, and it stays converted
	check(s.PutFile(File{Path: "/m/c.mkv", Size: 500}))
	c, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/c.mkv", SizeBefore: 500})
	check(err)
	s.Claim("w")
	check(s.Complete(c, "/m/c.mkv", 300))
	// a.mkv is in the library before it is queued: a scan records it first
	check(s.PutFile(File{Path: "/m/a.mkv", Size: 1000}))
	before := countsOf(t, s, l, w.ID, since, now)

	// a.mkv: converted, its original kept in the trash
	a, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	check(err)
	s.Claim("w")
	check(s.Complete(a, "/m/a.mkv", 400))
	ia, err := s.BeginSwap(Original{JobID: a, LibraryID: l.ID, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.mkv", NewPath: "/m/a.mkv"})
	check(err)
	check(s.FinishSwap(ia, now.Add(7*24*time.Hour)))
	converted := countsOf(t, s, l, w.ID, since, now)
	if reflect.DeepEqual(converted, before) {
		t.Fatal("the conversion changed no count: the test would prove nothing")
	}

	// restored: the original is back at its name (worker finishRestore: the row restored, the file recorded
	// as restored)
	check(s.SetSwapState(ia, OriginalRestored))
	check(s.PutFile(File{Path: "/m/a.mkv", Size: 1000, Decision: DecisionRestored, Reason: DecisionRestored}))
	after := countsOf(t, s, l, w.ID, since, now)
	for k := range before {
		if !reflect.DeepEqual(after[k], before[k]) {
			t.Errorf("%s after the restore = %+v, want %+v (as before the conversion)", k, after[k], before[k])
		}
	}
}

// A file converted twice whose newer conversion was restored is the older conversion's output again: it
// counts as that older conversion did before the newer one, not as nothing (review #431 P2).
func TestRestoringTheNewerConversionKeepsTheOlderOne(t *testing.T) {
	s := open(t)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.AddWorkflow("Fresh", workflow.Preset())
	check(err)
	d, _, err := s.Draft(w.ID)
	check(err)
	saved, err := s.SaveDraft(w.ID, d.Doc, workflow.Layout{}, d.Etag)
	check(err)
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	_, err = s.Publish(w.ID, saved.Etag, tm)
	check(err)
	l, err := s.AddLibraryOnFlow("Movies", "/m", w.ID, true)
	check(err)
	now := time.Now()
	since := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	// first conversion: 1000 -> 400, original trashed
	check(s.PutFile(File{Path: "/m/d.mkv", Size: 1000}))
	j1, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/d.mkv", SizeBefore: 1000})
	check(err)
	s.Claim("w")
	check(s.Complete(j1, "/m/d.mkv", 400))
	i1, err := s.BeginSwap(Original{JobID: j1, LibraryID: l.ID, Path: "/m/d.mkv", Trash: "/m/.syp-trash/d.mkv", NewPath: "/m/d.mkv"})
	check(err)
	check(s.FinishSwap(i1, now.Add(7*24*time.Hour)))
	first := countsOf(t, s, l, w.ID, since, now)

	// second conversion: 400 -> 300, then restored: the 400-byte file is back
	j2, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/d.mkv", SizeBefore: 400})
	check(err)
	s.Claim("w")
	check(s.Complete(j2, "/m/d.mkv", 300))
	i2, err := s.BeginSwap(Original{JobID: j2, LibraryID: l.ID, Path: "/m/d.mkv", Trash: "/m/.syp-trash/d.2.mkv", NewPath: "/m/d.mkv"})
	check(err)
	check(s.FinishSwap(i2, now.Add(7*24*time.Hour)))
	check(s.SetSwapState(i2, OriginalRestored))
	after := countsOf(t, s, l, w.ID, since, now)

	if n := after["done count"]; n != 1 {
		t.Errorf("done count after restoring the newer conversion = %v, want 1 (the older conversion is the file now)", n)
	}
	for _, k := range []string{"overview totals", "library totals"} {
		if !reflect.DeepEqual(after[k], first[k]) {
			t.Errorf("%s after restoring the newer conversion = %+v, want %+v (as after the older one)", k, after[k], first[k])
		}
	}
}
