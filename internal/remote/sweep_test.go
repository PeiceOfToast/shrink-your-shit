// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"os"
	"path/filepath"
	"testing"
)

// CT 221 (qa-fa019f5 incident): a worker killed with kill -9 left its lease folder; the next start
// removes leftover lease folders, and nothing else in its work folder.
func TestLeftoverLeaseFoldersGoAtStart(t *testing.T) {
	work := t.TempDir()
	lease := filepath.Join(work, "0123456789abcdef01234567")
	os.MkdirAll(filepath.Join(lease, "sub"), 0o700)
	os.WriteFile(filepath.Join(lease, "source.mkv"), make([]byte, 1024), 0o600)
	keep := map[string]bool{}
	for _, name := range []string{"0123456789ABCDEF01234567", "0123456789abcdef0123456", "notes", "0123456789abcdef0123456g"} {
		os.Mkdir(filepath.Join(work, name), 0o700) // upper case, too short, someone's folder, not hex
		keep[name] = true
	}
	os.WriteFile(filepath.Join(work, "fedcba9876543210fedcba98"), []byte("a file"), 0o600) // a lease-like name, but a file
	keep["fedcba9876543210fedcba98"] = true
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "theirs"), []byte("x"), 0o600)
	if err := os.Symlink(outside, filepath.Join(work, "aaaaaaaaaaaaaaaaaaaaaaaa")); err == nil { // a link with a lease name
		keep["aaaaaaaaaaaaaaaaaaaaaaaa"] = true
	}
	sweepLeases(work)
	if _, err := os.Lstat(lease); !os.IsNotExist(err) {
		t.Errorf("the leftover lease folder is still there: %v", err)
	}
	for name := range keep {
		if _, err := os.Lstat(filepath.Join(work, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "theirs")); err != nil {
		t.Errorf("followed the link: %v", err)
	}
	sweepLeases(filepath.Join(work, "none")) // no work folder yet: nothing to do
}
