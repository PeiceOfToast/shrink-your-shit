// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Folders that hold the computer's own files, or every user's, are never a library (slice 23, SAF-4);
// the folders people keep videos in are fine. Each system's list is checked on every system.
func TestSystemFolders(t *testing.T) {
	for _, c := range []struct {
		goos, home, p string
		want          bool
	}{
		{"linux", "/home/ann", "/", true},
		{"linux", "/home/ann", "/home", true},
		{"linux", "/home/ann", "/home/ann", true},
		{"linux", "/home/ann", "/home/ann/", true},
		{"linux", "/home/ann", "/home/ann/Videos", false},
		{"linux", "/home/ann", "/etc", true},
		{"linux", "/home/ann", "/usr/share/videos", true},
		{"linux", "/home/ann", "/var", true},
		{"linux", "/home/ann", "/var/lib/media", false},
		{"linux", "/home/ann", "/srv/media", false},
		{"linux", "/home/ann", "/mnt/disk1/Movies", false},
		{"linux", "/home/ann", "/etcetera", false},
		{"linux", "/root", "/root", true},
		{"darwin", "/Users/ann", "/Users", true},
		{"darwin", "/Users/ann", "/users/ANN", true},
		{"darwin", "/Users/ann", "/Users/ann/Movies", false},
		{"darwin", "/Users/ann", "/System/Library", true},
		{"darwin", "/Users/ann", "/Volumes", true},
		{"darwin", "/Users/ann", "/Volumes/Media/Films", false},
		{"windows", `C:\Users\ann`, `C:\`, true},
		{"windows", `C:\Users\ann`, `C:`, true},
		{"windows", `C:\Users\ann`, `c:\users`, true},
		{"windows", `C:\Users\ann`, `C:\Users\Ann`, true},
		{"windows", `C:\Users\ann`, `C:\Users\ann\Videos`, false},
		{"windows", `C:\Users\ann`, `C:\Windows\System32`, true},
		{"windows", `C:\Users\ann`, `C:\Program Files (x86)\x`, true},
		{"windows", `C:\Users\ann`, `C:\ProgramData`, true},
		{"windows", `C:\Users\ann`, `D:\`, false}, // a whole data drive is a normal place for videos (lead 13:59Z)
		{"windows", `C:\Users\ann`, `E:`, false},
		{"windows", `C:\Users\ann`, `\\nas\media`, false}, // a NAS share root too
		{"windows", `C:\Users\ann`, `\\nas\media\Movies`, false},
		{"windows", `C:\Users\ann`, `\\nas`, true}, // a bare host, no share
		{"windows", `C:\Users\ann`, `/`, true},     // rooted, no drive: the system drive's root
		{"windows", `C:\Users\ann`, `\`, true},
		{"windows", `C:\Users\ann`, `C:/`, true},
		{"windows", `C:\Users\ann`, `c:\`, true},
		{"windows", `C:\Users\ann`, `/Windows`, true},
		{"windows", `C:\Users\ann`, `\Movies`, false},
		{"windows", `C:\Users\ann`, `\\?\C:\`, true}, // the device namespace: other names (review 75e86eb)
		{"windows", `C:\Users\ann`, `\\.\C:\`, true},
		{"windows", `C:\Users\ann`, `\\?\C:\Windows\System32`, true},
		{"windows", `C:\Users\ann`, `\\?\D:\Movies`, true}, // any device path, even to a fine folder
		{"windows", `C:\Users\ann`, `D:\Movies`, false},
	} {
		if got := systemFolder(c.p, c.goos, c.home, "", ""); got != c.want {
			t.Errorf("%s %q: %v, want %v", c.goos, c.p, got, c.want)
		}
	}
}

// The store refuses them however a library would be made or moved there, and does not start one made
// there before.
func TestStoreRefusesSystemFolders(t *testing.T) {
	s := open(t)
	if _, err := s.AddLibrary("Everything", SystemDriveRootForTest(), rules.SliderTemplate(3), true); !errors.Is(err, ErrSystemFolder) {
		t.Errorf("add /: %v", err)
	}
	l, _ := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	root := SystemDriveRootForTest()
	if _, err := s.UpdateLibrary(l.ID, nil, &root); !errors.Is(err, ErrSystemFolder) {
		t.Errorf("move to /: %v", err)
	}
	if _, skipped, _ := s.EnsureLibraries([]string{SystemDirForTest()}, rules.SliderTemplate(3)); len(skipped) != 1 {
		t.Errorf("-library %s: %v", SystemDirForTest(), skipped)
	}
	s.db.Exec(`UPDATE libraries SET path=? WHERE id=?`, SystemDirForTest(), l.ID) // made before the guard existed
	if err := s.StartLibrary(l.ID); !errors.Is(err, ErrSystemFolder) {
		t.Errorf("start at /etc: %v", err)
	}
}

// The real system folders of the system running the tests are refused through the exported check.
func TestSystemFolderOnThisSystem(t *testing.T) {
	for _, p := range []string{"/", SystemDriveRootForTest(), SystemDirForTest()} {
		if !SystemFolder(p) {
			t.Errorf("%s is not refused", p)
		}
	}
	if SystemFolder(t.TempDir()) {
		t.Error("a temp folder is refused")
	}
}

func TestDevicePath(t *testing.T) {
	for p, want := range map[string]bool{`\\?\C:\`: true, `\\.\C:\`: true, `//?/D:/x`: true, `\\nas\share`: false, `D:\`: false, `/home/x`: false} {
		if DevicePath(p) != want {
			t.Errorf("%q: want %v", p, want)
		}
	}
}
