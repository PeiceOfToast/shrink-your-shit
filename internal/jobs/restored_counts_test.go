// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// A restored original is the original file again, so it is Skipped (reason 'restored'), never Done. After the
// restore, every surface that counts the file agrees: Done loses it, Skipped gains it, by the same amount.
func TestRestoredFileIsSkippedEverywhere(t *testing.T) {
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

	// c.mkv stays converted, so the counts are not all zero
	check(s.PutFile(File{Path: "/m/c.mkv", Size: 500}))
	c, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/c.mkv", SizeBefore: 500})
	check(err)
	s.Claim("w")
	check(s.Complete(c, "/m/c.mkv", 300))

	// a.mkv: converted, its original kept in the trash
	check(s.PutFile(File{Path: "/m/a.mkv", Size: 1000}))
	a, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	check(err)
	s.Claim("w")
	check(s.Complete(a, "/m/a.mkv", 400))
	ia, err := s.BeginSwap(Original{JobID: a, LibraryID: l.ID, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.mkv", NewPath: "/m/a.mkv"})
	check(err)
	check(s.FinishSwap(ia, now.Add(7*24*time.Hour)))

	before := surfacesOf(t, s, l, since, now)

	// restored: the original is back at its name (as worker finishRestore does)
	check(s.SetSwapState(ia, OriginalRestored))
	check(s.PutFile(File{Path: "/m/a.mkv", Size: 1000, Decision: DecisionRestored, Reason: DecisionRestored}))

	after := surfacesOf(t, s, l, since, now)
	want := map[string]int{
		"jobs done":           -1, // the Jobs tab Done count
		"done count":          -1, // the overview's converted number
		"library done":        -1,
		"done list":           -1, // /api/activity/done total
		"jobs skipped":        +1, // the Jobs tab Skipped count
		"library skipped":     +1,
		"daily skipped":       +1,
		"summary skipped 48h": +1,
	}
	for k, delta := range want {
		if got := after[k] - before[k]; got != delta {
			t.Errorf("%s changed by %d after the restore, want %d (before %d, after %d)", k, got, delta, before[k], after[k])
		}
	}
	for _, id := range doneIDs(t, s) {
		if id == a {
			t.Errorf("the Done list still has the restored job %d", a)
		}
	}
}

// surfacesOf reads the file counts each screen shows, keyed by the screen's name.
func surfacesOf(t *testing.T, s *Store, l Library, since string, now time.Time) map[string]int {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	tot, err := s.TotalsOf()
	check(err)
	lib, err := s.LibraryTotalsOf(l)
	check(err)
	days, err := s.Days(7, now, tot)
	check(err)
	done, err := s.DoneCount()
	check(err)
	_, doneCards, _, err := s.FileList(FileFilter{Show: CardProcessed})
	check(err)
	_, skippedFiles, _, err := s.FileList(FileFilter{Show: CardSkipped})
	check(err)
	ends, err := s.JobEndsSince(since)
	check(err)
	_, doneTotal, err := s.DoneItems(50, 0)
	check(err)
	return map[string]int{
		"jobs done":           doneCards,
		"done count":          done,
		"library done":        lib.Done,
		"done list":           doneTotal,
		"jobs skipped":        skippedFiles,
		"library skipped":     lib.Skipped,
		"daily skipped":       days[len(days)-1].Skipped,
		"summary skipped 48h": ends["skipped"] + ends["cancelled"],
	}
}

// doneIDs is the job ids on the activity Done list.
func doneIDs(t *testing.T, s *Store) []int64 {
	t.Helper()
	items, _, err := s.DoneItems(50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, it := range items {
		ids = append(ids, it.JobID)
	}
	return ids
}
