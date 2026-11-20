// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// doneOriginal records a finished job whose 1000-byte original is at name in the library, with a 400-byte output
// beside it. The original stays on disk until the test removes it.
func doneOriginal(t *testing.T, st *jobs.Store, l jobs.Library, name string) string {
	t.Helper()
	p := filepath.Join(l.Path, name)
	os.WriteFile(p, make([]byte, 1000), 0o644)
	id, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: p, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	st.Claim("w")
	st.Complete(id, p[:len(p)-4]+".hevc.mkv", 400)
	return p
}

// scanLibrary walks the one library once, as the server's scan does.
func scanLibrary(s *Scanner, l jobs.Library) {
	tmpl := rules.LegacyTemplate(rules.Default(), "replace")
	s.Targets = func() ([]Target, error) {
		return []Target{{LibraryID: l.ID, Path: l.Path, Template: tmpl}}, nil
	}
	s.ScanOnce(context.Background())
}

// A folder that had videos at the last scan and is empty now (a bare mount point, say) sets no gone marks and keeps
// the count it had.
func TestEmptyFolderAfterANonEmptyScanSetsNoGoneMarks(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	doneOriginal(t, st, l, "kept.mkv")
	scanLibrary(s, l)
	before, _ := st.GetLibrary(l.ID)
	if before.LastScanVideos == 0 {
		t.Fatal("the first scan found no videos; the test needs some")
	}
	for _, name := range []string{"kept.mkv", "a.mkv", "notes.txt", "b.syp-tmp.mkv"} {
		os.Remove(filepath.Join(l.Path, name)) // the folder is empty now
	}
	scanLibrary(s, l)
	after, _ := st.GetLibrary(l.ID)
	if after.LastScanVideos != before.LastScanVideos {
		t.Errorf("last_scan_videos %d after the empty walk; want it kept at %d", after.LastScanVideos, before.LastScanVideos)
	}
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 0 {
		t.Errorf("freed %d after an empty folder; want 0: no gone marks", c[l.ID].FreedBytes)
	}
}

// Positive control for the test above: an original that really is gone is counted as freed.
func TestRemovedOriginalIsMarkedGone(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	gone := doneOriginal(t, st, l, "gone.mkv")
	os.Remove(gone)
	scanLibrary(s, l)
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 600 {
		t.Fatalf("freed %d; want 600 for a removed original", c[l.ID].FreedBytes)
	}
}

// An original marked gone that is back, at the same path and still the original, clears its mark.
func TestOriginalThatComesBackClearsItsGoneMark(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	gone := doneOriginal(t, st, l, "back.mkv")
	os.Remove(gone)
	scanLibrary(s, l)
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 600 {
		t.Fatalf("freed %d before it came back; want 600", c[l.ID].FreedBytes)
	}
	os.WriteFile(gone, make([]byte, 1000), 0o644) // the same original, back
	scanLibrary(s, l)
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 0 {
		t.Errorf("freed %d after the original came back; want 0: the gone mark clears", c[l.ID].FreedBytes)
	}
}

// A folder that still holds other files when its videos go is recorded as it is: the gone marks are set.
func TestDeletedVideosWithOtherFilesLeftStillMarkGone(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	kept := doneOriginal(t, st, l, "kept.mkv")
	scanLibrary(s, l)
	os.Remove(kept)
	os.Remove(filepath.Join(l.Path, "a.mkv"))
	scanLibrary(s, l) // notes.txt is still there
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 600 {
		t.Errorf("freed %d after the videos were deleted from a folder with other files; want 600", c[l.ID].FreedBytes)
	}
}
