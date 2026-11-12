// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V5a: the folder beside a library, "<library folder> (shrunk)", or design's words for why there is none.
func TestNextToLibrary(t *testing.T) {
	parent := t.TempDir()
	lib := filepath.Join(parent, "movies")
	os.Mkdir(lib, 0o755)
	if dir, p := NextToLibrary("Movies", lib); p != nil || dir != filepath.Join(parent, "movies (shrunk)") {
		t.Fatalf("%q %+v", dir, p)
	}
	old := sameDisk
	sameDisk = func(a, b string) bool { return false } // the library is a mount point
	_, p := NextToLibrary("Movies", lib)
	sameDisk = old
	if want := "No folder for new files for Movies yet: " + parent + " is on a different disk. Pick a folder in the library's Output tab, or choose One folder for all."; p == nil || p.Code != NoDefaultFolder || p.Message != want || p.Folder != parent || p.Reason != ReasonDifferentDisk {
		t.Errorf("another disk: %+v", p)
	}
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(parent) + `\`
	}
	if _, p := NextToLibrary("Media", filepath.Join(root, "media")); p == nil || p.Message != "No folder for new files for Media yet: "+root+" is the container's own disk. Pick a folder in the library's Output tab, or choose One folder for all." || p.Folder != root || p.Reason != ReasonContainerRoot {
		t.Errorf("container root: %+v", p)
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 { // read-only folders can't be tested as root, or on Windows by mode
		os.Chmod(parent, 0o555)
		defer os.Chmod(parent, 0o755)
		if _, p := NextToLibrary("Movies", lib); p == nil || p.Message != "No folder for new files for Movies yet: "+parent+" can't be written to. Pick a folder in the library's Output tab, or choose One folder for all." || p.Folder != parent || p.Reason != ReasonNotWritable {
			t.Errorf("read-only parent: %+v", p)
		}
		os.Chmod(parent, 0o755)
		os.Mkdir(lib+" (shrunk)", 0o755) // made earlier: it is what must be writable, not the parent
		os.Chmod(parent, 0o555)
		if dir, p := NextToLibrary("Movies", lib); p != nil || dir != lib+" (shrunk)" {
			t.Errorf("existing folder in a read-only parent: %q %+v", dir, p)
		}
	}
}

// A library's override folder wins; "" goes back to the default.
func TestOutputFolderOverride(t *testing.T) {
	s := open(t)
	lib, _ := s.AddLibrary("Movies", filepath.Join(t.TempDir(), "movies"), rules.SliderTemplate(3), true)
	os.MkdirAll(lib.Path, 0o755)
	if dir, p, err := s.OutputFolder(lib.ID, lib.Path); err != nil || p != nil || dir != lib.Path+" (shrunk)" {
		t.Fatalf("default %q %+v %v", dir, p, err)
	}
	own := t.TempDir()
	s.SetOverrideFolder(lib.ID, own)
	if dir, _, _ := s.OutputFolder(lib.ID, lib.Path); dir != own {
		t.Errorf("override %q", dir)
	}
	s.SetOverrideFolder(lib.ID, "")
	if dir, _, _ := s.OutputFolder(lib.ID, lib.Path); dir != lib.Path+" (shrunk)" {
		t.Errorf("back to the default %q", dir)
	}
	if err := s.SetOverrideFolder(999, own); err != ErrNotFound {
		t.Errorf("no such library: %v", err)
	}
}
