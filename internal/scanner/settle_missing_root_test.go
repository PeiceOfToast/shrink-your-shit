// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library whose folder is missing (a disk not mounted) is not walked whole, so its settling rows stay. They are
// forgotten only once the folder is back and the file is really gone.
func TestSettlingRowSurvivesMissingLibraryFolder(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock, clock)
	s.ScanOnce(context.Background()) // young: settling
	if r, _ := st.GetFile(a); r == nil || r.Reason != rules.SkipStillChanging {
		t.Fatalf("no settling row for a young file: %+v", r)
	}

	os.RemoveAll(lib) // the folder is missing: the walk finds nothing
	s.ScanOnce(context.Background())
	if r, _ := st.GetFile(a); r == nil {
		t.Error("settling row forgotten while its library folder is missing")
	}

	os.Mkdir(lib, 0o755) // back, and the file gone; another old video there, so the walk finds videos and is not a bare mount point
	c := filepath.Join(lib, "c.mkv")
	os.WriteFile(c, []byte("12345"), 0o644)
	os.Chtimes(c, clock.Add(-time.Hour), clock.Add(-time.Hour))
	s.ScanOnce(context.Background())
	if r, _ := st.GetFile(a); r != nil {
		t.Errorf("settling row of a file gone from a back folder kept: %+v", r)
	}
}

// A missing library folder is not a walk whole for any post-walk step: its originals are not marked gone, and its
// last scan (count and time) is kept, not overwritten by an empty walk.
func TestMissingFolderMarksNothingGoneAndKeepsLastScan(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	doneOriginal(t, st, l, "kept.mkv")
	scanLibrary(s, l)
	before, _ := st.GetLibrary(l.ID)
	c0, _ := st.Counts()
	if before.LastScanVideos == 0 {
		t.Fatal("the first scan found no videos; the test needs some")
	}

	os.RemoveAll(l.Path) // the folder is missing (a disk not mounted)
	scanLibrary(s, l)
	after, _ := st.GetLibrary(l.ID)
	if after.LastScanVideos != before.LastScanVideos || after.LastScan != before.LastScan {
		t.Errorf("last scan %d at %q after a missing folder; want %d at %q kept",
			after.LastScanVideos, after.LastScan, before.LastScanVideos, before.LastScan)
	}
	c1, _ := st.Counts()
	if c1[l.ID].FreedBytes != c0[l.ID].FreedBytes || c1[l.ID].CouldFreeBytes != c0[l.ID].CouldFreeBytes {
		t.Errorf("freed %d could %d after a missing folder; want %d and %d: no gone marks",
			c1[l.ID].FreedBytes, c1[l.ID].CouldFreeBytes, c0[l.ID].FreedBytes, c0[l.ID].CouldFreeBytes)
	}
}
