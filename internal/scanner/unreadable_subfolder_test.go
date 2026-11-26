// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A subfolder of a readable library that cannot be read is not a walk whole: its files are not seen, and that
// must not look like files the person deleted. The library's count and time, its freed space and the files'
// state in the subfolder are left as they were, and the next scan tries again.
func TestUnreadableSubfolderKeepsItsFilesState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not stop reads on Windows or as root")
	}
	for _, mode := range []os.FileMode{0o000, 0o311, 0o644} { // 000: nothing; 0311: search but not read; 0644: list but not search
		t.Run(map[os.FileMode]string{0o000: "000", 0o311: "0311", 0o644: "0644"}[mode], func(t *testing.T) {
			s, st, _, _ := setup(t, hevcBig)
			libs, _ := st.Libraries()
			l := libs[0]
			doneOriginal(t, st, l, "kept.mkv")
			sub := filepath.Join(l.Path, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			inner := filepath.Join(sub, "inner.mkv")
			os.WriteFile(inner, make([]byte, 1000), 0o644)
			id, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: inner, SizeBefore: 1000})
			if err != nil {
				t.Fatal(err)
			}
			st.Claim("w")
			st.Complete(id, inner[:len(inner)-4]+".hevc.mkv", 400)
			// a file not decided yet: its first look is the state an unreadable folder must not drop
			fresh := filepath.Join(sub, "fresh.mkv")
			os.WriteFile(fresh, make([]byte, 2000), 0o644)
			scanLibrary(s, l)
			before, _ := st.GetLibrary(l.ID)
			c0, _ := st.Counts()
			if _, ok := s.firstLook[fresh]; !ok {
				t.Fatal("the readable scan kept no first look for the fresh file; the test needs one")
			}
			if before.LastScanVideos < 2 {
				t.Fatalf("the first scan found %d videos; the test needs both", before.LastScanVideos)
			}

			// the subfolder can't be read: the file inside it is still there, so nothing in it may be marked gone
			if err := os.Chmod(sub, mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(sub, 0o755) }) // so TempDir can be removed
			scanLibrary(s, l)
			after, _ := st.GetLibrary(l.ID)
			if after.LastScanVideos != before.LastScanVideos || after.LastScan != before.LastScan {
				t.Errorf("last scan %d at %q after an unreadable subfolder; want %d at %q kept",
					after.LastScanVideos, after.LastScan, before.LastScanVideos, before.LastScan)
			}
			c1, _ := st.Counts()
			if _, ok := s.firstLook[fresh]; !ok {
				t.Errorf("the first look of a file in the unreadable subfolder was dropped")
			}
			if c1[l.ID].FreedBytes != c0[l.ID].FreedBytes || c1[l.ID].CouldFreeBytes != c0[l.ID].CouldFreeBytes {
				t.Errorf("freed %d could %d after an unreadable subfolder; want %d and %d: no gone marks",
					c1[l.ID].FreedBytes, c1[l.ID].CouldFreeBytes, c0[l.ID].FreedBytes, c0[l.ID].CouldFreeBytes)
			}

			// readable again: the file inside is still there, so the next scan records the library as it is
			if err := os.Chmod(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			scanLibrary(s, l)
			again, _ := st.GetLibrary(l.ID)
			if again.LastScanVideos != before.LastScanVideos {
				t.Errorf("after the subfolder is readable again: last scan %d; want %d", again.LastScanVideos, before.LastScanVideos)
			}
			if c2, _ := st.Counts(); c2[l.ID].FreedBytes != c0[l.ID].FreedBytes {
				t.Errorf("freed %d after the subfolder is readable again; want %d: nothing was deleted", c2[l.ID].FreedBytes, c0[l.ID].FreedBytes)
			}
		})
	}
}
