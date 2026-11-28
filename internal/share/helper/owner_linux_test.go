// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"os"
	"path/filepath"
	"testing"
)

// The shares folder is refused when anything above it can be written by another user: that user
// could swap the folder for a link and steer the helper's mkdir, chown and mount elsewhere.
func TestCheckOwnedPath(t *testing.T) {
	me := uint32(os.Getuid())
	tmp := t.TempDir() // under sticky /tmp; the umask may leave it group-writable
	if err := os.Chmod(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(tmp, "lib")
	shares := filepath.Join(base, "shares")
	if err := os.MkdirAll(shares, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkOwnedPath(shares, me); err != nil {
		t.Fatalf("an owner-only path refused: %v", err)
	}
	if err := checkOwnedPath(shares, me+1); err == nil && me != 0 {
		t.Error("a path owned by another uid passed")
	}
	if err := os.Chmod(base, 0o775); err != nil { // the parent group-writable, like /var/lib/syp for syp
		t.Fatal(err)
	}
	if err := checkOwnedPath(shares, me); err == nil {
		t.Error("a group-writable parent passed")
	}
	if err := os.Chmod(base, os.ModeSticky|0o777); err != nil { // sticky: others can't rename what they don't own
		t.Fatal(err)
	}
	if err := checkOwnedPath(shares, me); err != nil {
		t.Errorf("a sticky parent refused: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(shares, link); err != nil {
		t.Fatal(err)
	}
	if err := checkOwnedPath(link, me); err == nil {
		t.Error("a symbolic link passed")
	}
}
