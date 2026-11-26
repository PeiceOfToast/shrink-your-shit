// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A folder that is always unreadable and never media (a disk's lost+found, say) must not stop its library being
// recorded: the library would freeze, and originals deleted from it would never count as freed. The walk does not
// read lost+found at all.
func TestUnreadableLostFoundStillRecordsTheLibrary(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not stop reads on Windows or as root")
	}
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	doneOriginal(t, st, l, "kept.mkv")
	lf := filepath.Join(l.Path, "lost+found")
	if err := os.Mkdir(lf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lf, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lf, 0o755) }) // so TempDir can be removed
	scanLibrary(s, l)
	before, _ := st.GetLibrary(l.ID)
	if before.LastScanVideos == 0 {
		t.Fatal("the first scan recorded no videos; the test needs some")
	}

	os.Remove(filepath.Join(l.Path, "kept.mkv")) // a real deletion, with lost+found still unreadable
	scanLibrary(s, l)
	after, _ := st.GetLibrary(l.ID)
	if after.LastScanVideos != before.LastScanVideos-1 {
		t.Errorf("last scan %d videos after the original was deleted; want %d recorded: an unreadable lost+found froze the library",
			after.LastScanVideos, before.LastScanVideos-1)
	}
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 600 {
		t.Errorf("freed %d after the original was deleted; want 600", c[l.ID].FreedBytes)
	}
}

// A folder on the flow's Ignore list is not looked at, so an unreadable one there is never read, and the library is
// recorded as usual. This pins that the Ignore list is the way out for any other always-unreadable folder.
func TestUnreadableIgnoredFolderStillRecordsTheLibrary(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod does not stop reads on Windows or as root")
	}
	s, st, _, _ := setup(t, hevcBig)
	libs, _ := st.Libraries()
	l := libs[0]
	doneOriginal(t, st, l, "kept.mkv")
	sub := filepath.Join(l.Path, "private")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) }) // so TempDir can be removed

	sel := rules.DefaultSelectors(3)
	sel.Ignore = []string{"private"}
	tmpl := sel.Template()
	s.Targets = func() ([]Target, error) {
		return []Target{{LibraryID: l.ID, Path: l.Path, Template: tmpl}}, nil
	}
	s.ScanOnce(context.Background())
	before, _ := st.GetLibrary(l.ID)
	if before.LastScanVideos == 0 {
		t.Fatal("the first scan recorded no videos; the test needs some")
	}

	os.Remove(filepath.Join(l.Path, "kept.mkv"))
	s.ScanOnce(context.Background())
	after, _ := st.GetLibrary(l.ID)
	if after.LastScanVideos != before.LastScanVideos-1 {
		t.Errorf("last scan %d videos after the original was deleted; want %d recorded: an ignored unreadable folder froze the library",
			after.LastScanVideos, before.LastScanVideos-1)
	}
	if c, _ := st.Counts(); c[l.ID].FreedBytes != 600 {
		t.Errorf("freed %d after the original was deleted; want 600", c[l.ID].FreedBytes)
	}
}
