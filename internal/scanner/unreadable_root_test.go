// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"runtime"
	"testing"
)

// A library whose root folder cannot be read (a permissions slip) is not a walk whole: it finds nothing, and that
// must not look like a library that emptied. Its count, its time and its gone marks are left as they were.
func TestUnreadableLibraryRootKeepsItsState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not stop reads on Windows or as root")
	}
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

	if err := os.Chmod(l.Path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(l.Path, 0o755) }) // so TempDir can be removed
	scanLibrary(s, l)
	after, _ := st.GetLibrary(l.ID)
	if after.LastScanVideos != before.LastScanVideos || after.LastScan != before.LastScan {
		t.Errorf("last scan %d at %q after an unreadable root; want %d at %q kept",
			after.LastScanVideos, after.LastScan, before.LastScanVideos, before.LastScan)
	}
	c1, _ := st.Counts()
	if c1[l.ID].FreedBytes != c0[l.ID].FreedBytes || c1[l.ID].CouldFreeBytes != c0[l.ID].CouldFreeBytes {
		t.Errorf("freed %d could %d after an unreadable root; want %d and %d: no gone marks",
			c1[l.ID].FreedBytes, c1[l.ID].CouldFreeBytes, c0[l.ID].FreedBytes, c0[l.ID].CouldFreeBytes)
	}

	// Readable again: the next scan records the library again.
	if err := os.Chmod(l.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	scanLibrary(s, l)
	again, _ := st.GetLibrary(l.ID)
	if again.LastScanVideos != before.LastScanVideos || again.LastScan == "" {
		t.Errorf("after the root is readable again: last scan %d at %q; want %d recorded",
			again.LastScanVideos, again.LastScan, before.LastScanVideos)
	}
}
